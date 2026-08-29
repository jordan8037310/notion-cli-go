package cmd

import (
	"bytes"
	"encoding/json"
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

		// Cobra-standard boolean spellings — #67. Anything strconv.ParseBool
		// recognises as true must suppress; anything it recognises as false
		// must NOT suppress (explicit-off should keep the banner the same as
		// no flag at all).
		{"json_equals_true", []string{"notioncli", "list", "--json=true"}, true},
		{"json_equals_True", []string{"notioncli", "list", "--json=True"}, true},
		{"json_equals_TRUE", []string{"notioncli", "list", "--json=TRUE"}, true},
		{"json_equals_1", []string{"notioncli", "list", "--json=1"}, true},
		{"json_equals_t", []string{"notioncli", "list", "--json=t"}, true},
		{"json_equals_false", []string{"notioncli", "list", "--json=false"}, false},
		{"json_equals_0", []string{"notioncli", "list", "--json=0"}, false},
		{"json_equals_garbage", []string{"notioncli", "list", "--json=maybe"}, false},
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

// TestJSONMode_SingleErrorLine guards issue #64. jsonErrorOr writes a
// one-line JSON error envelope to stderr and then returns the error;
// cobra's default handler also printed "Error: ..." plus the usage block
// to the same stream, so a JSON-mode failure produced two outputs for one
// error and broke any consumer treating stderr as line-delimited JSON.
// PersistentPreRunE now silences cobra's own printing in JSON mode.
//
// `fetch` is the command under test on purpose: the blocks subcommands
// already set SilenceErrors inside their own RunE, so they never
// double-printed and would make this test vacuous.
func TestJSONMode_SingleErrorLine(t *testing.T) {
	m := withFetchEnv(t)
	m.pageOK = false
	m.dbOK = false
	resetRootCmdArgs()
	t.Cleanup(resetGlobalOutputFlags)

	var stderr bytes.Buffer
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	// Both probes 404 → the dispatcher fails through jsonErrorOr.
	rootCmd.SetArgs([]string{"fetch", "--json", fetchHexID})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected an error when neither page nor database resolves, got nil")
	}

	lines := []string{}
	for _, l := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("json mode emitted %d stderr lines, want exactly 1 (the JSON envelope):\n%s",
			len(lines), stderr.String())
	}
	var env map[string]string
	if err := json.Unmarshal([]byte(lines[0]), &env); err != nil {
		t.Fatalf("stderr line is not a JSON error envelope: %v (%q)", err, lines[0])
	}
	if env["error"] == "" {
		t.Errorf("JSON error envelope has no error field: %q", lines[0])
	}
	if strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("json mode leaked cobra's usage block: %q", stderr.String())
	}
}

// TestTextMode_KeepsCobraErrorOutput is the counterpart: silencing is
// scoped to JSON mode, so text-mode failures still print cobra's
// human-readable "Error: ..." line. It also proves the JSON-mode
// silencing does not leak across invocations — resetGlobalOutputFlags
// restores the root command's flags.
func TestTextMode_KeepsCobraErrorOutput(t *testing.T) {
	m := withFetchEnv(t)
	m.pageOK = false
	m.dbOK = false

	// Run once in JSON mode first, so a leaked silence flag would show up.
	resetRootCmdArgs()
	var discard bytes.Buffer
	rootCmd.SetOut(&discard)
	rootCmd.SetErr(&discard)
	rootCmd.SetArgs([]string{"fetch", "--json", fetchHexID})
	_ = rootCmd.Execute()

	resetRootCmdArgs()
	t.Cleanup(resetGlobalOutputFlags)
	var stderr bytes.Buffer
	rootCmd.SetOut(&stderr)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"fetch", fetchHexID})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected an error when neither page nor database resolves, got nil")
	}
	if !strings.Contains(stderr.String(), "Error:") {
		t.Errorf("text mode should still print cobra's Error line; got %q", stderr.String())
	}
}
