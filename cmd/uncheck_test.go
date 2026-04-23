package cmd

import (
	"bytes"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
)

// TestUncheckCmdArgsAndFlags asserts argument arity and that the --order
// flag is declared as int with a default of 0.
func TestUncheckCmdArgsAndFlags(t *testing.T) {
	var c *cobra.Command
	for _, cc := range rootCmd.Commands() {
		if cc.Name() == "uncheck" {
			c = cc
			break
		}
	}
	if c == nil {
		t.Fatal("uncheck command not registered on rootCmd")
	}

	cases := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"zero rejected", []string{}, true},
		{"one accepted", []string{"1"}, false},
		{"three rejected", []string{"1", "2", "3"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := c.Args(c, tc.args); (err != nil) != tc.wantErr {
				t.Errorf("uncheckCmd.Args(%v) err=%v wantErr=%v", tc.args, err, tc.wantErr)
			}
		})
	}

	f := c.Flag("order")
	if f == nil {
		t.Fatal("uncheck: --order flag not registered")
	}
	if f.DefValue != "0" {
		t.Errorf("uncheck: --order default = %q, want %q", f.DefValue, "0")
	}
	if f.Value.Type() != "int" {
		t.Errorf("uncheck: --order type = %q, want %q", f.Value.Type(), "int")
	}
}

// TestUncheckCmdDispatch drives `notioncli uncheck 1` and confirms the
// expected GET (resolve block id) + PATCH (clear checked) pair hits the
// mock Notion API.
func TestUncheckCmdDispatch(t *testing.T) {
	srv := withCmdEnv(t)

	var listed, patched int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/blocks/pageID/children"):
			atomic.AddInt64(&listed, 1)
		case r.Method == http.MethodPatch && r.URL.Path == "/blocks/blockID":
			atomic.AddInt64(&patched, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	resetRootCmdArgs(t)
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"uncheck", "1"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(uncheck 1): %v", err)
	}

	if atomic.LoadInt64(&listed) == 0 {
		t.Error("uncheck command did not list children to resolve the block id")
	}
	if atomic.LoadInt64(&patched) == 0 {
		t.Error("uncheck command did not PATCH /blocks/blockID")
	}
}

// TestUncheckCmdNonNumericArg is a negative dispatch test: passing a
// non-integer positional arg should trip the strconv.Atoi branch. The
// command prints an error and calls os.Exit(1) in that path, so we cannot
// safely call Execute() here. Instead, exercise the ParseArgs code path by
// confirming cobra still accepts the arg shape (cobra.ExactArgs(1) only
// checks count), leaving the conversion to Run.
func TestUncheckCmdNonNumericArgAcceptedByCobra(t *testing.T) {
	var c *cobra.Command
	for _, cc := range rootCmd.Commands() {
		if cc.Name() == "uncheck" {
			c = cc
			break
		}
	}
	if c == nil {
		t.Fatal("uncheck command not registered on rootCmd")
	}
	// Cobra's ExactArgs doesn't type-check positionals — that's the Run
	// function's job. This test pins that contract so a future refactor
	// that adds a type validator still preserves the expected error
	// surface.
	if err := c.Args(c, []string{"not-a-number"}); err != nil {
		t.Errorf("uncheck: expected cobra.Args to accept any single positional, got %v", err)
	}
}
