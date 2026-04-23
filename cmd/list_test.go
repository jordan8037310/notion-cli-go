package cmd

import (
	"bytes"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// TestListCmdMetadata asserts the listCmd is registered and wears its
// documented Use/Short strings. Flag-parsing assertions for list are thin
// because list has no flags today — this test guards against a regression
// that silently drops the command or strips its help text.
func TestListCmdMetadata(t *testing.T) {
	var found bool
	for _, c := range rootCmd.Commands() {
		if c.Name() == "list" {
			found = true
			if c.Short == "" {
				t.Error("list: Short description is empty")
			}
			if c.Long == "" {
				t.Error("list: Long description is empty")
			}
			if c.Run == nil {
				t.Error("list: Run function not set")
			}
			break
		}
	}
	if !found {
		t.Fatal("list command not registered on rootCmd")
	}
}

// TestListCmdDispatch invokes `notioncli list` end-to-end with the HTTP
// client redirected at a mock Notion API. The mock handler is wrapped to
// tally hits so we can assert the list path was actually exercised —
// output goes to fmt.Println (stdout), which cmd.SetOut cannot intercept,
// so we rely on mock-hit counts rather than captured stdout.
func TestListCmdDispatch(t *testing.T) {
	srv := withCmdEnv(t)

	var hits int64
	origHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/blocks/pageID/children") {
			atomic.AddInt64(&hits, 1)
		}
		origHandler.ServeHTTP(w, r)
	})

	resetRootCmdArgs(t)
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"list"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(list): %v", err)
	}

	if atomic.LoadInt64(&hits) == 0 {
		t.Error("list command did not hit /blocks/pageID/children")
	}
}
