package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestExecute is a smoke test: it confirms the command tree builds and that
// invoking --help returns without panicking. The real Execute() calls
// os.Exit on error, so we drive rootCmd directly to keep the test process
// alive.
func TestExecute(t *testing.T) {
	rootCmd.SetArgs([]string{"--help"})
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute: %v", err)
	}
	if !strings.Contains(out.String(), "notioncli") {
		t.Errorf("help output missing command name; got:\n%s", out.String())
	}
}

// TestRootCmdSubcommandsRegistered verifies every user-facing subcommand is
// wired into the root command. Adding a new command without registering it
// should fail this test immediately.
func TestRootCmdSubcommandsRegistered(t *testing.T) {
	want := []string{"list", "add", "check", "uncheck", "delete", "blocks"}
	got := make(map[string]bool)
	for _, c := range rootCmd.Commands() {
		got[c.Name()] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("subcommand %q not registered on rootCmd", name)
		}
	}
}

// TestExecuteHappyPath drives the exported Execute() function through the
// --help branch so its happy path is covered. osExit is swapped out for a
// recorder so an unexpected error path cannot terminate the test binary.
func TestExecuteHappyPath(t *testing.T) {
	var exited bool
	var code int
	origExit := osExit
	osExit = func(c int) {
		exited = true
		code = c
	}
	t.Cleanup(func() { osExit = origExit })

	rootCmd.SetArgs([]string{"--help"})
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	Execute()

	if exited {
		t.Errorf("Execute() unexpectedly called osExit(%d) on happy path", code)
	}
}

// TestShouldSuppressBanner_JSONForms exercises the banner-suppression
// rule: suppress when --json or --output=json (any space/equals form),
// do NOT suppress for --output=text / --output text / plain invocations.
// This locks the fix for the PR #28 review: bare --output with a non-
// json value was incorrectly swallowing the banner.
func TestShouldSuppressBanner_JSONForms(t *testing.T) {
	orig := os.Args
	t.Cleanup(func() { os.Args = orig })

	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"plain", []string{"notioncli", "list"}, false},
		{"json_flag", []string{"notioncli", "list", "--json"}, true},
		{"output_equals_json", []string{"notioncli", "list", "--output=json"}, true},
		{"output_space_json", []string{"notioncli", "list", "--output", "json"}, true},
		{"output_equals_text", []string{"notioncli", "list", "--output=text"}, false},
		{"output_space_text", []string{"notioncli", "list", "--output", "text"}, false},
		{"output_trailing_no_value", []string{"notioncli", "list", "--output"}, false},
		{"help", []string{"notioncli", "--help"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			os.Args = tc.args
			if got := shouldSuppressBanner(); got != tc.want {
				t.Errorf("shouldSuppressBanner() = %v, want %v (args=%v)", got, tc.want, tc.args)
			}
		})
	}
}

// TestExecuteErrorPath verifies the Execute wrapper forwards a non-nil
// rootCmd error to osExit(1). We force the error by passing an unknown
// subcommand and intercept osExit to avoid killing the test process.
func TestExecuteErrorPath(t *testing.T) {
	var exited bool
	var code int
	origExit := osExit
	osExit = func(c int) {
		exited = true
		code = c
	}
	t.Cleanup(func() { osExit = origExit })

	rootCmd.SetArgs([]string{"definitely-not-a-real-subcommand"})
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	Execute()

	if !exited {
		t.Fatal("Execute() did not call osExit on error path")
	}
	if code != 1 {
		t.Errorf("Execute() osExit code = %d, want 1", code)
	}
}
