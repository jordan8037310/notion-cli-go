package cmd

import (
	"bytes"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
)

// TestAddCmdArgsValidation exercises the cobra-level argument validation
// without invoking the Run function. `add` requires exactly one positional
// argument — zero args and two args must fail; one arg must pass.
func TestAddCmdArgsValidation(t *testing.T) {
	// Locate the actual command instance via rootCmd so we match what users hit.
	var addC *cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Name() == "add" {
			addC = c
			break
		}
	}
	if addC == nil {
		t.Fatal("add command not registered on rootCmd")
	}

	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"zero args rejected", []string{}, true},
		{"one arg accepted", []string{"buy milk"}, false},
		{"two args rejected", []string{"buy", "milk"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := addC.Args(addC, tt.args); (err != nil) != tt.wantErr {
				t.Errorf("addCmd.Args(%v) err=%v wantErr=%v", tt.args, err, tt.wantErr)
			}
		})
	}
}

// TestAddCmdDispatch runs `notioncli add "hello world"` end-to-end against
// the mock Notion API and asserts a PATCH fired at /blocks/pageID/children
// (the Notion endpoint that appends new children, which is what
// utils.AddNewToDoItem calls).
func TestAddCmdDispatch(t *testing.T) {
	srv := withCmdEnv(t)

	var patched int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/blocks/pageID/children") {
			atomic.AddInt64(&patched, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	resetRootCmdArgs(t)
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"add", "buy milk"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(add): %v", err)
	}

	if atomic.LoadInt64(&patched) == 0 {
		t.Error("add command did not PATCH /blocks/pageID/children")
	}
}
