package cmd

import (
	"bytes"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// TestDeleteCmdArgsAndFlags asserts ExactArgs(1) and the --order flag
// declaration. `delete` is the to-do deletion wired onto rootCmd (the
// blocks delete subcommand is tested in blocks_test.go).
func TestDeleteCmdArgsAndFlags(t *testing.T) {
	// Walk top-level rootCmd children only — don't descend into blocksCmd.
	c := findTopLevelCmd(t, "delete")

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
				t.Errorf("deleteCmd.Args(%v) err=%v wantErr=%v", tc.args, err, tc.wantErr)
			}
		})
	}

	f := c.Flag("order")
	if f == nil {
		t.Fatal("delete: --order flag not registered")
	}
	if f.DefValue != "0" {
		t.Errorf("delete: --order default = %q, want %q", f.DefValue, "0")
	}
	if f.Value.Type() != "int" {
		t.Errorf("delete: --order type = %q, want %q", f.Value.Type(), "int")
	}
}

// TestDeleteCmdDispatch runs `notioncli delete 1` and confirms the listing
// + DELETE pair hit the mock server.
func TestDeleteCmdDispatch(t *testing.T) {
	srv := withCmdEnv(t)

	var listed, deleted int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/blocks/pageID/children"):
			atomic.AddInt64(&listed, 1)
		case r.Method == http.MethodDelete && r.URL.Path == "/blocks/blockID":
			atomic.AddInt64(&deleted, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	resetRootCmdArgs()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"delete", "1"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(delete 1): %v", err)
	}

	if atomic.LoadInt64(&listed) == 0 {
		t.Error("delete command did not list children")
	}
	if atomic.LoadInt64(&deleted) == 0 {
		t.Error("delete command did not DELETE /blocks/blockID")
	}
}
