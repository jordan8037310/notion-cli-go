package cmd

import (
	"bytes"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
)

// TestCheckCmdArgsAndFlags asserts that `check` requires exactly one
// positional arg, and that the declared `--order` flag exists with its
// default of 0.
func TestCheckCmdArgsAndFlags(t *testing.T) {
	var c *cobra.Command
	for _, cc := range rootCmd.Commands() {
		if cc.Name() == "check" {
			c = cc
			break
		}
	}
	if c == nil {
		t.Fatal("check command not registered on rootCmd")
	}

	// Args validation.
	cases := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"zero rejected", []string{}, true},
		{"one accepted", []string{"1"}, false},
		{"two rejected", []string{"1", "2"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := c.Args(c, tc.args); (err != nil) != tc.wantErr {
				t.Errorf("checkCmd.Args(%v) err=%v wantErr=%v", tc.args, err, tc.wantErr)
			}
		})
	}

	// Flag declaration: --order Int, default 0.
	f := c.Flag("order")
	if f == nil {
		t.Fatal("check: --order flag not registered")
	}
	if f.DefValue != "0" {
		t.Errorf("check: --order default = %q, want %q", f.DefValue, "0")
	}
	if f.Value.Type() != "int" {
		t.Errorf("check: --order type = %q, want %q", f.Value.Type(), "int")
	}
}

// TestCheckCmdDispatch runs `notioncli check 1` and asserts the expected
// request shape hit the mock Notion API: a GET to list children followed
// by a PATCH to the target block id.
func TestCheckCmdDispatch(t *testing.T) {
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
	rootCmd.SetArgs([]string{"check", "1"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(check 1): %v", err)
	}

	if atomic.LoadInt64(&listed) == 0 {
		t.Error("check command did not list children to resolve the block id")
	}
	if atomic.LoadInt64(&patched) == 0 {
		t.Error("check command did not PATCH /blocks/blockID")
	}
}
