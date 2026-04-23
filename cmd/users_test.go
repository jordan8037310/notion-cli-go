package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"notioncli/utils"

	"github.com/spf13/cobra"
)

// findUsersSubcommand walks the users command's children and returns the
// child matching name, or fails the test.
func findUsersSubcommand(t *testing.T, name string) *cobra.Command {
	t.Helper()
	usersC := findTopLevelCmd(t, "users")
	for _, c := range usersC.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("users subcommand %q not found", name)
	return nil
}

// withUsersEnv is like withCmdEnv but the mock server understands the
// /v1/users endpoints instead of /v1/blocks. Kept in this file (rather
// than testhelpers_test.go) because scope for this change is limited to
// cmd/users_test.go and cmd/teams_test.go.
func withUsersEnv(t *testing.T) *httptest.Server {
	t.Helper()

	writeJSON := func(w http.ResponseWriter, v interface{}) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/users":
			writeJSON(w, utils.UserList{
				Object: "list",
				Results: []utils.User{
					{Object: "user", ID: "u1", Type: "person", Name: "Ada", Person: &utils.UserPerson{Email: "ada@example.com"}},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/users/me":
			writeJSON(w, utils.User{Object: "user", ID: "bot-1", Type: "bot", Name: "CLI bot", Bot: &utils.UserBot{WorkspaceName: "Acme"}})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/users/"):
			writeJSON(w, utils.User{Object: "user", ID: strings.TrimPrefix(r.URL.Path, "/users/"), Type: "person", Name: "Ada"})
		default:
			http.Error(w, `{"object":"error","code":"not_found"}`, http.StatusNotFound)
		}
	}))

	priorBaseURL := utils.GetBaseURL()
	utils.SetBaseURL(srv.URL)
	t.Cleanup(func() {
		utils.SetBaseURL(priorBaseURL)
		srv.Close()
	})

	emptyCwd := t.TempDir()
	emptyHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(emptyCwd, ".env"), []byte(""), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(emptyCwd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	t.Setenv("HOME", emptyHome)
	t.Setenv("NOTION_API_KEY", "test-key")
	t.Setenv("NOTION_PAGE_ID", "pageID")
	t.Setenv("LOCAL_TIMEZONE", "UTC")

	return srv
}

// TestUsersCmdRegistered verifies `users` is a top-level command with
// the three subcommands the issue requires.
func TestUsersCmdRegistered(t *testing.T) {
	usersC := findTopLevelCmd(t, "users")
	want := map[string]bool{"list": false, "get": false, "whoami": false}
	for _, c := range usersC.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("users subcommand %q not registered", name)
		}
	}
}

// TestUsersListFlags asserts the list subcommand declares a --json flag
// with default false.
func TestUsersListFlags(t *testing.T) {
	list := findUsersSubcommand(t, "list")
	f := list.Flag("json")
	if f == nil {
		t.Fatal("users list: --json flag not registered")
	}
	if f.DefValue != "false" {
		t.Errorf("users list: --json default = %q, want false", f.DefValue)
	}
	if f.Value.Type() != "bool" {
		t.Errorf("users list: --json type = %q, want bool", f.Value.Type())
	}
}

// TestUsersGetArgs asserts the get subcommand requires exactly one
// positional argument.
func TestUsersGetArgs(t *testing.T) {
	get := findUsersSubcommand(t, "get")

	if err := get.Args(get, []string{}); err == nil {
		t.Error("users get: expected error on zero args, got nil")
	}
	if err := get.Args(get, []string{"abc"}); err != nil {
		t.Errorf("users get: expected no error on one arg, got %v", err)
	}
	if err := get.Args(get, []string{"a", "b"}); err == nil {
		t.Error("users get: expected error on two args, got nil")
	}

	f := get.Flag("json")
	if f == nil {
		t.Fatal("users get: --json flag not registered")
	}
}

// TestUsersWhoamiFlags asserts the whoami subcommand has --json wired.
func TestUsersWhoamiFlags(t *testing.T) {
	whoami := findUsersSubcommand(t, "whoami")
	f := whoami.Flag("json")
	if f == nil {
		t.Fatal("users whoami: --json flag not registered")
	}
}

// TestUsersListDispatch runs `notioncli users list --json` and asserts
// the list GET was issued and the output is parseable JSON.
func TestUsersListDispatch(t *testing.T) {
	srv := withUsersEnv(t)

	var listed int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/users" {
			atomic.AddInt64(&listed, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	// Reset shared JSON flag so prior tests don't leak state.
	usersJSON = false

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"users", "list", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(users list): %v", err)
	}
	if atomic.LoadInt64(&listed) == 0 {
		t.Error("users list did not hit /users")
	}

	// JSON body should decode as a []User.
	body := bytes.TrimSpace(out.Bytes())
	if len(body) == 0 {
		t.Fatal("users list --json: empty output")
	}
	var decoded []utils.User
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Errorf("users list --json: output is not a []User: %v\n%s", err, body)
	}
}

// TestUsersGetDispatch runs `notioncli users get u1` and asserts the
// per-id GET was issued.
func TestUsersGetDispatch(t *testing.T) {
	srv := withUsersEnv(t)

	var got int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/users/u1" {
			atomic.AddInt64(&got, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	usersJSON = false
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"users", "get", "u1"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(users get): %v", err)
	}
	if atomic.LoadInt64(&got) == 0 {
		t.Error("users get did not hit /users/u1")
	}
}

// TestUsersWhoamiDispatch runs `notioncli users whoami --json` and
// asserts /users/me was hit.
func TestUsersWhoamiDispatch(t *testing.T) {
	srv := withUsersEnv(t)

	var hit int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/users/me" {
			atomic.AddInt64(&hit, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	usersJSON = false
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"users", "whoami", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(users whoami): %v", err)
	}
	if atomic.LoadInt64(&hit) == 0 {
		t.Error("users whoami did not hit /users/me")
	}

	body := bytes.TrimSpace(out.Bytes())
	if len(body) == 0 {
		t.Fatal("users whoami --json: empty output")
	}
	var decoded utils.User
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Errorf("users whoami --json: output is not a User: %v\n%s", err, body)
	}
}
