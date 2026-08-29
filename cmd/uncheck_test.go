package cmd

import (
	"bytes"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// TestUncheckCmdArgsAndFlags asserts argument arity and that the --order
// flag is declared as int with a default of 0.
func TestUncheckCmdArgsAndFlags(t *testing.T) {
	c := findTopLevelCmd(t, "uncheck")

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

	resetRootCmdArgs()
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
