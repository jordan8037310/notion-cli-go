package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"notioncli/utils"

	"github.com/fatih/color"
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

// defaultUsersHandler serves the standard /v1/users fixtures shared by
// the happy-path dispatch tests. It is factored out so error-path tests
// can construct a server with a replacement handler while reusing the
// same env scaffolding.
func defaultUsersHandler() http.Handler {
	writeJSON := func(w http.ResponseWriter, v interface{}) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	})
}

// withUsersEnv is like withCmdEnv but the mock server understands the
// /v1/users endpoints instead of /v1/blocks. Kept in this file (rather
// than testhelpers_test.go) because scope for this change is limited to
// cmd/users_test.go and cmd/teams_test.go.
func withUsersEnv(t *testing.T) *httptest.Server {
	t.Helper()
	return withUsersEnvHandler(t, defaultUsersHandler())
}

// withUsersEnvHandler is the overlay-friendly form: callers pass in a
// replacement handler (typically one that simulates errors) and the
// helper takes care of all the cwd/env scaffolding. This mirrors the
// pattern recommended for sibling PRs so error branches are easy to
// reach without duplicating the env setup.
func withUsersEnvHandler(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(h)

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

// recordOSExit swaps osExit with a recorder for the duration of the
// test. Returns pointers so callers can assert on the captured code.
// Tests using this helper must not rely on osExit actually terminating
// the goroutine; the Cobra Run closure returns after osExit per the
// existing `return` statements in cmd/users.go.
func recordOSExit(t *testing.T) (*bool, *int) {
	t.Helper()
	exited := false
	code := 0
	orig := osExit
	osExit = func(c int) {
		exited = true
		code = c
	}
	t.Cleanup(func() { osExit = orig })
	return &exited, &code
}

// captureColorOutput redirects fatih/color's package-level Output and
// Error writers into the given io.Writer for the duration of the test.
// color.Red / color.Yellow etc. write to color.Output (not the cobra
// command's stdout), so tests that need to assert on that output must
// swap it out explicitly. Restored in t.Cleanup.
func captureColorOutput(t *testing.T, w io.Writer) {
	t.Helper()
	origOut := color.Output
	origErr := color.Error
	color.Output = w
	color.Error = w
	t.Cleanup(func() {
		color.Output = origOut
		color.Error = origErr
	})
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
	resetGlobalOutputFlags()

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

	// users list --json emits NDJSON (one User object per line), not a
	// JSON array. The shape change is deliberate and matches the other
	// list commands (blocks, comments, teams, databases query, search).
	body := bytes.TrimSpace(out.Bytes())
	if len(body) == 0 {
		t.Fatal("users list --json: empty output")
	}
	for i, line := range bytes.Split(body, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var u utils.User
		if err := json.Unmarshal(line, &u); err != nil {
			t.Errorf("users list --json: line %d is not a User: %v\n%s", i, err, line)
		}
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

	resetGlobalOutputFlags()
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

// errorHandler returns an http.Handler that responds to every request
// with the given status code and a minimal Notion-shaped error body.
func errorHandler(status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"object":"error","code":"unauthorized"}`, status)
	})
}

// TestUsersListDispatch_HTTPErrorExits asserts that a non-2xx response
// from the Notion users endpoint surfaces as a returned error. The
// command migrated from Run+osExit to RunE, so the error is returned
// from rootCmd.Execute and cobra's default exit path handles it.
func TestUsersListDispatch_HTTPErrorExits(t *testing.T) {
	withUsersEnvHandler(t, errorHandler(http.StatusUnauthorized))

	var out bytes.Buffer
	captureColorOutput(t, &out)

	resetGlobalOutputFlags()
	resetRootCmdArgs()
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"users", "list"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("users list: expected error on HTTP 401, got nil")
	}
	if !strings.Contains(err.Error(), "users list") {
		t.Errorf("users list: expected 'users list' in error, got %v", err)
	}
}

// TestUsersGetDispatch_HTTPErrorExits covers the 404 path for
// `users get <id>`.
func TestUsersGetDispatch_HTTPErrorExits(t *testing.T) {
	withUsersEnvHandler(t, errorHandler(http.StatusNotFound))

	var out bytes.Buffer
	captureColorOutput(t, &out)

	resetGlobalOutputFlags()
	resetRootCmdArgs()
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"users", "get", "does-not-exist"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("users get: expected error on HTTP 404, got nil")
	}
	if !strings.Contains(err.Error(), "users get") {
		t.Errorf("users get: expected 'users get' in error, got %v", err)
	}
}

// TestUsersWhoamiDispatch_HTTPErrorExits covers the 401 path for
// `users whoami`.
func TestUsersWhoamiDispatch_HTTPErrorExits(t *testing.T) {
	withUsersEnvHandler(t, errorHandler(http.StatusUnauthorized))

	var out bytes.Buffer
	captureColorOutput(t, &out)

	resetGlobalOutputFlags()
	resetRootCmdArgs()
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"users", "whoami"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("users whoami: expected error on HTTP 401, got nil")
	}
	if !strings.Contains(err.Error(), "users whoami") {
		t.Errorf("users whoami: expected 'users whoami' in error, got %v", err)
	}
}

// TestUsersListDispatch_HumanOutput runs `notioncli users list` WITHOUT
// --json and asserts the rendered rows contain each user's key fields.
// Covers cmd/users.go:62-68 (the empty-check + formatUser loop).
func TestUsersListDispatch_HumanOutput(t *testing.T) {
	withUsersEnvHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/users" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(utils.UserList{
				Object: "list",
				Results: []utils.User{
					{Object: "user", ID: "u1", Type: "person", Name: "Ada", Person: &utils.UserPerson{Email: "ada@example.com"}},
					{Object: "user", ID: "u2", Type: "person", Name: "Grace", Person: &utils.UserPerson{Email: "grace@example.com"}},
					{Object: "user", ID: "bot-1", Type: "bot", Name: "CLI bot", Bot: &utils.UserBot{WorkspaceName: "Acme"}},
				},
			})
			return
		}
		http.Error(w, `{"object":"error","code":"not_found"}`, http.StatusNotFound)
	}))

	resetGlobalOutputFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"users", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(users list): %v", err)
	}

	// Assert that each user's key fields land on stdout exactly once.
	// Stick to key substrings rather than exact byte-matching the whole
	// line so the formatter is free to tweak cosmetics.
	got := out.String()
	for _, want := range []string{"Ada", "ada@example.com", "Grace", "grace@example.com", "CLI bot", "Acme", "u1", "u2", "bot-1"} {
		if c := strings.Count(got, want); c != 1 {
			t.Errorf("users list human output: want substring %q exactly once, got %d\noutput:\n%s", want, c, got)
		}
	}
}

// TestUsersListDispatch_HumanOutputEmpty covers the "No users returned."
// branch (cmd/users.go:62-65) when the Notion API returns an empty list.
func TestUsersListDispatch_HumanOutputEmpty(t *testing.T) {
	withUsersEnvHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(utils.UserList{Object: "list", Results: []utils.User{}})
	}))

	exited, _ := recordOSExit(t)
	var out bytes.Buffer
	captureColorOutput(t, &out)

	resetGlobalOutputFlags()
	resetRootCmdArgs()
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"users", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(users list): %v", err)
	}

	if *exited {
		t.Error("users list: osExit was unexpectedly called on an empty list")
	}
	if !strings.Contains(out.String(), "No users returned") {
		t.Errorf("users list (empty): want substring 'No users returned' in:\n%s", out.String())
	}
}

// TestUsersGetDispatch_HumanOutput runs `users get u1` without --json
// and asserts formatUser produced output containing the id and name.
// Covers cmd/users.go:96.
func TestUsersGetDispatch_HumanOutput(t *testing.T) {
	withUsersEnv(t)

	resetGlobalOutputFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"users", "get", "u1"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(users get): %v", err)
	}
	got := out.String()
	for _, want := range []string{"u1", "Ada"} {
		if !strings.Contains(got, want) {
			t.Errorf("users get human output: want substring %q in:\n%s", want, got)
		}
	}
}

// TestUsersWhoamiDispatch_HumanOutput runs `users whoami` without
// --json and asserts formatUser produced output for the bot user.
// Covers cmd/users.go:123.
func TestUsersWhoamiDispatch_HumanOutput(t *testing.T) {
	withUsersEnv(t)

	resetGlobalOutputFlags()
	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"users", "whoami"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(users whoami): %v", err)
	}
	got := out.String()
	for _, want := range []string{"bot-1", "CLI bot", "Acme"} {
		if !strings.Contains(got, want) {
			t.Errorf("users whoami human output: want substring %q in:\n%s", want, got)
		}
	}
}

// TestFormatUser is a table-driven unit test that drives every branch
// of the formatUser switch without going through HTTP. Covers the
// unnamed-fallback and missing-type paths that the dispatch tests
// don't reach.
func TestFormatUser(t *testing.T) {
	cases := []struct {
		name string
		in   utils.User
		want []string // substrings the formatted string must contain
	}{
		{
			name: "person with email",
			in:   utils.User{ID: "u1", Type: "person", Name: "Ada", Person: &utils.UserPerson{Email: "ada@example.com"}},
			want: []string{"[person]", "Ada", "<ada@example.com>", "id=u1"},
		},
		{
			name: "bot with workspace",
			in:   utils.User{ID: "bot-1", Type: "bot", Name: "CLI bot", Bot: &utils.UserBot{WorkspaceName: "Acme"}},
			want: []string{"[bot]", "CLI bot", "(workspace: Acme)", "id=bot-1"},
		},
		{
			name: "unnamed person falls back to placeholder",
			in:   utils.User{ID: "u2", Type: "person"},
			want: []string{"[person]", "(unnamed)", "id=u2"},
		},
		{
			name: "missing type falls back to user",
			in:   utils.User{ID: "u3", Name: "Anon"},
			want: []string{"[user]", "Anon", "id=u3"},
		},
		{
			name: "person with nil email does not add detail",
			in:   utils.User{ID: "u4", Type: "person", Name: "Nilmail", Person: &utils.UserPerson{}},
			want: []string{"[person]", "Nilmail", "id=u4"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatUser(tc.in)
			for _, sub := range tc.want {
				if !strings.Contains(got, sub) {
					t.Errorf("formatUser = %q; missing %q", got, sub)
				}
			}
		})
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

	resetGlobalOutputFlags()
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
