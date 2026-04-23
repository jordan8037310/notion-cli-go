package cmd

import (
	"bytes"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
)

// findBlocksSubcommand walks the blocks command's children and returns the
// child matching name, or fails the test.
func findBlocksSubcommand(t *testing.T, name string) *cobra.Command {
	t.Helper()
	var blocksC *cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Name() == "blocks" {
			blocksC = c
			break
		}
	}
	if blocksC == nil {
		t.Fatal("blocks command not registered on rootCmd")
	}
	for _, c := range blocksC.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("blocks subcommand %q not found", name)
	return nil
}

// TestBlocksCmdRegistered verifies `blocks` is a top-level command and
// owns its three subcommands (list, add, delete).
func TestBlocksCmdRegistered(t *testing.T) {
	var blocksC *cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Name() == "blocks" {
			blocksC = c
			break
		}
	}
	if blocksC == nil {
		t.Fatal("blocks command not registered on rootCmd")
	}

	want := map[string]bool{"list": false, "add": false, "delete": false}
	for _, c := range blocksC.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("blocks subcommand %q not registered", name)
		}
	}
}

// TestBlocksListFlags asserts the list subcommand declares a --type/-t flag
// whose default is the empty string (no filter).
func TestBlocksListFlags(t *testing.T) {
	list := findBlocksSubcommand(t, "list")

	long := list.Flag("type")
	if long == nil {
		t.Fatal("blocks list: --type flag not registered")
	}
	if long.Shorthand != "t" {
		t.Errorf("blocks list: --type shorthand = %q, want %q", long.Shorthand, "t")
	}
	if long.DefValue != "" {
		t.Errorf("blocks list: --type default = %q, want empty", long.DefValue)
	}
	if long.Value.Type() != "string" {
		t.Errorf("blocks list: --type type = %q, want string", long.Value.Type())
	}
}

// TestBlocksAddFlags asserts the add subcommand declares --type/-t with
// default "paragraph" and that it takes at least one positional arg.
func TestBlocksAddFlags(t *testing.T) {
	add := findBlocksSubcommand(t, "add")

	long := add.Flag("type")
	if long == nil {
		t.Fatal("blocks add: --type flag not registered")
	}
	if long.Shorthand != "t" {
		t.Errorf("blocks add: --type shorthand = %q, want %q", long.Shorthand, "t")
	}
	if long.DefValue != "paragraph" {
		t.Errorf("blocks add: --type default = %q, want %q", long.DefValue, "paragraph")
	}

	// MinimumNArgs(1) means zero args must error.
	if err := add.Args(add, []string{}); err == nil {
		t.Error("blocks add: expected error on zero args, got nil")
	}
	if err := add.Args(add, []string{"hello"}); err != nil {
		t.Errorf("blocks add: expected no error on one arg, got %v", err)
	}
}

// TestBlocksDeleteArgs asserts the delete subcommand requires exactly one
// positional arg.
func TestBlocksDeleteArgs(t *testing.T) {
	del := findBlocksSubcommand(t, "delete")

	if err := del.Args(del, []string{}); err == nil {
		t.Error("blocks delete: expected error on zero args, got nil")
	}
	if err := del.Args(del, []string{"1"}); err != nil {
		t.Errorf("blocks delete: expected no error on one arg, got %v", err)
	}
	if err := del.Args(del, []string{"1", "2"}); err == nil {
		t.Error("blocks delete: expected error on two args, got nil")
	}
}

// TestBlocksListDispatch runs `notioncli blocks list` and confirms the
// listing call hit the mock server.
func TestBlocksListDispatch(t *testing.T) {
	srv := withCmdEnv(t)

	var listed int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/blocks/pageID/children") {
			atomic.AddInt64(&listed, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	// Reset any --type filter that a previous test may have set. blockType
	// is a package-level variable; a stale "to_do" here would narrow results.
	blockType = ""

	resetRootCmdArgs(t)
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(blocks list): %v", err)
	}

	if atomic.LoadInt64(&listed) == 0 {
		t.Error("blocks list did not hit /blocks/pageID/children")
	}
}

// TestBlocksAddDispatch runs `notioncli blocks add "hi" -t paragraph` and
// asserts the add PATCH was issued.
func TestBlocksAddDispatch(t *testing.T) {
	srv := withCmdEnv(t)

	var patched int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/blocks/pageID/children") {
			atomic.AddInt64(&patched, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	// Reset block type so the add command falls back to paragraph.
	blockType = ""

	resetRootCmdArgs(t)
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "add", "hello world", "-t", "paragraph"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(blocks add): %v", err)
	}

	if atomic.LoadInt64(&patched) == 0 {
		t.Error("blocks add did not PATCH /blocks/pageID/children")
	}
}

// TestBlocksDeleteDispatch runs `notioncli blocks delete 1` and asserts
// that both a listing (to resolve the target id) and a DELETE were issued.
func TestBlocksDeleteDispatch(t *testing.T) {
	srv := withCmdEnv(t)

	var listed, deleted int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/blocks/pageID/children"):
			atomic.AddInt64(&listed, 1)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/blocks/"):
			atomic.AddInt64(&deleted, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	blockType = ""

	resetRootCmdArgs(t)
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"blocks", "delete", "1"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(blocks delete): %v", err)
	}

	if atomic.LoadInt64(&listed) == 0 {
		t.Error("blocks delete did not list children")
	}
	if atomic.LoadInt64(&deleted) == 0 {
		t.Error("blocks delete did not DELETE a block")
	}
}
