// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// stubWatchWait replaces the real timer with one that lets the loop run
// exactly n times and then reports "stop", so the loop is driven
// deterministically instead of racing a clock.
func stubWatchWait(t *testing.T, n int) *int {
	t.Helper()
	prior := watchWait
	calls := 0
	watchWait = func(ctx context.Context, d time.Duration) error {
		calls++
		if calls >= n {
			return context.Canceled
		}
		return nil
	}
	t.Cleanup(func() { watchWait = prior })
	return &calls
}

// TestParseWatchInterval covers the values a caller can pass.
func TestParseWatchInterval(t *testing.T) {
	for _, tc := range []struct {
		in      string
		wantErr string
	}{
		{in: "30s"},
		{in: "5m"},
		{in: "1h"},
		{in: "1s"},
		{in: "banana", wantErr: "not a duration"},
		{in: "30", wantErr: "not a duration"},
		{in: "", wantErr: "not a duration"},
		// Sub-second polling is a non-goal: Notion rate-limits at a few
		// requests per second, so it returns the same data and spends the
		// caller's quota.
		{in: "100ms", wantErr: "below the 1s minimum"},
		{in: "0s", wantErr: "below the 1s minimum"},
		{in: "-5s", wantErr: "below the 1s minimum"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			_, err := parseWatchInterval(tc.in)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("parseWatchInterval(%q) = %v, want no error", tc.in, err)
			case tc.wantErr != "" && err == nil:
				t.Errorf("parseWatchInterval(%q) succeeded, want %q", tc.in, tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Errorf("parseWatchInterval(%q) = %q, want it to mention %q", tc.in, err, tc.wantErr)
			}
		})
	}
}

// TestWatchLoop_UnsetRunsExactlyOnce is the no-regression case: with the
// flag absent the wrapper must be invisible.
func TestWatchLoop_UnsetRunsExactlyOnce(t *testing.T) {
	resetGlobalOutputFlags()
	runs := 0
	cmd := &cobra.Command{Use: "x"}
	cmd.SetOut(&bytes.Buffer{})
	err := watchLoop(cmd, nil, func(*cobra.Command, []string) error {
		runs++
		return nil
	})
	if err != nil {
		t.Fatalf("watchLoop: %v", err)
	}
	if runs != 1 {
		t.Errorf("ran %d times with --watch unset, want 1", runs)
	}
}

// TestWatchLoop_RepeatsUntilStopped drives the loop through several
// iterations and checks it ends cleanly rather than returning an error —
// being asked to stop is not a failure.
func TestWatchLoop_RepeatsUntilStopped(t *testing.T) {
	resetGlobalOutputFlags()
	globalWatch = "1s"
	t.Cleanup(resetGlobalOutputFlags)
	stubWatchWait(t, 3)

	runs := 0
	var out bytes.Buffer
	cmd := &cobra.Command{Use: "x"}
	cmd.SetOut(&out)
	if err := watchLoop(cmd, nil, func(*cobra.Command, []string) error {
		runs++
		fmt.Fprintf(&out, "render %d\n", runs)
		return nil
	}); err != nil {
		t.Fatalf("watchLoop returned %v, want nil — an interrupted watch exited as asked", err)
	}
	if runs != 3 {
		t.Errorf("ran %d times, want 3", runs)
	}
}

// TestWatchLoop_StopsOnError pins the decision that a failing iteration
// ends the watch. The transport already retries what is worth retrying, so
// an error reaching here is one more polling will not fix — looping on it
// would print the same error forever.
func TestWatchLoop_StopsOnError(t *testing.T) {
	resetGlobalOutputFlags()
	globalWatch = "1s"
	t.Cleanup(resetGlobalOutputFlags)
	stubWatchWait(t, 10)

	sentinel := errors.New("token revoked")
	runs := 0
	cmd := &cobra.Command{Use: "x"}
	cmd.SetOut(&bytes.Buffer{})
	err := watchLoop(cmd, nil, func(*cobra.Command, []string) error {
		runs++
		if runs == 2 {
			return sentinel
		}
		return nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("watchLoop returned %v, want the iteration's error surfaced", err)
	}
	if runs != 2 {
		t.Errorf("ran %d times, want the loop to stop at the failing iteration (2)", runs)
	}
}

// TestWatchLoop_RejectsBadInterval fails before running anything, so a
// typo does not fetch once and then error.
func TestWatchLoop_RejectsBadInterval(t *testing.T) {
	resetGlobalOutputFlags()
	globalWatch = "banana"
	t.Cleanup(resetGlobalOutputFlags)

	runs := 0
	cmd := &cobra.Command{Use: "x"}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := watchLoop(cmd, nil, func(*cobra.Command, []string) error { runs++; return nil })
	if err == nil {
		t.Fatal("watchLoop accepted an unparseable --watch")
	}
	if runs != 0 {
		t.Errorf("ran %d times despite an invalid interval", runs)
	}
}

// TestWatchLoop_SeparatorOnlyInTextMode is the NDJSON contract. A piped
// human render gets a separator so the accumulated output stays readable;
// a piped JSON render must not, or the stream stops being valid NDJSON and
// `--json --watch | jq` breaks — which is the combination the flag exists
// to support.
func TestWatchLoop_SeparatorOnlyInTextMode(t *testing.T) {
	t.Run("text mode separates renders", func(t *testing.T) {
		resetGlobalOutputFlags()
		globalWatch = "1s"
		t.Cleanup(resetGlobalOutputFlags)
		stubWatchWait(t, 3)

		var out bytes.Buffer
		cmd := &cobra.Command{Use: "x"}
		cmd.SetOut(&out)
		_ = watchLoop(cmd, nil, func(c *cobra.Command, _ []string) error {
			fmt.Fprintln(c.OutOrStdout(), "row")
			return nil
		})
		got := 0
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(line, "--- ") {
				got++
			}
		}
		if got != 2 {
			t.Errorf("got %d separators for 3 renders, want 2 (between them, not before the first):\n%s", got, out.String())
		}
	})

	t.Run("json mode stays valid NDJSON", func(t *testing.T) {
		resetGlobalOutputFlags()
		globalWatch = "1s"
		globalJSON = true
		t.Cleanup(resetGlobalOutputFlags)
		stubWatchWait(t, 3)

		var out bytes.Buffer
		cmd := &cobra.Command{Use: "x"}
		cmd.SetOut(&out)
		_ = watchLoop(cmd, nil, func(c *cobra.Command, _ []string) error {
			return emitJSON(c.OutOrStdout(), map[string]string{"id": "x"})
		})
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if len(lines) != 3 {
			t.Fatalf("got %d lines for 3 renders, want 3:\n%s", len(lines), out.String())
		}
		for i, line := range lines {
			var obj map[string]interface{}
			if err := json.Unmarshal([]byte(line), &obj); err != nil {
				t.Errorf("line %d is not JSON (%v): %q — a separator broke the stream", i, err, line)
			}
		}
	})
}

// TestWatchable_MarksAndWraps checks the two halves stay together: the
// annotation and the loop are installed by the same call, so a command can
// never advertise --watch without implementing it.
func TestWatchable_MarksAndWraps(t *testing.T) {
	resetGlobalOutputFlags()
	globalWatch = "1s"
	t.Cleanup(resetGlobalOutputFlags)
	stubWatchWait(t, 2)

	runs := 0
	cmd := watchable(&cobra.Command{
		Use:  "x",
		RunE: func(*cobra.Command, []string) error { runs++; return nil },
	})
	if cmd.Annotations[watchAnnotation] == "" {
		t.Error("watchable did not mark the command as supporting --watch")
	}
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if runs != 2 {
		t.Errorf("ran %d times, want the loop installed (2)", runs)
	}
}

// TestRejectUnsupportedWatch refuses the flag where it would do nothing.
// Silently ignoring it is the wrong failure: the caller sits watching a
// terminal that never refreshes.
func TestRejectUnsupportedWatch(t *testing.T) {
	resetGlobalOutputFlags()
	t.Cleanup(resetGlobalOutputFlags)

	unsupported, _, err := rootCmd.Find([]string{"pages", "get"})
	if err != nil {
		t.Fatalf("find pages get: %v", err)
	}

	globalWatch = ""
	if err := rejectUnsupportedWatch(unsupported); err != nil {
		t.Errorf("rejected an invocation with no --watch: %v", err)
	}

	globalWatch = "30s"
	err = rejectUnsupportedWatch(unsupported)
	if err == nil {
		t.Fatal("accepted --watch on a command that does not loop")
	}
	if !strings.Contains(err.Error(), "pages get") {
		t.Errorf("error = %q, want it to name the offending command", err)
	}
	// The message must list the commands that DO support it, so the next
	// attempt works.
	for _, want := range []string{"notioncli list", "notioncli search", "notioncli blocks list"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

// TestWatchableCommands_CoversTheIssueList asserts every command #46 names
// actually accepts the flag. A command silently dropping off this list is
// the regression that would make --watch look broken.
func TestWatchableCommands_CoversTheIssueList(t *testing.T) {
	got := strings.Join(watchableCommands(rootCmd), ",")
	for _, want := range []string{
		"notioncli list",
		"notioncli blocks list",
		"notioncli databases query",
		"notioncli search",
		"notioncli comments list",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q does not accept --watch; watchable commands are %s", want, got)
		}
	}
}

// TestIsTerminal keeps cursor escapes out of redirected output.
func TestIsTerminal(t *testing.T) {
	if isTerminal(&bytes.Buffer{}) {
		t.Error("a buffer was reported as a terminal; screen-clear escapes would be written into a pipe")
	}
}

// TestValidateWatchFlag_ReportsOnStderr is a regression test for a bug the
// unit tests could not have found: `search --watch 100ms` exited 1 having
// written nothing to either stream, because searchCmd sets SilenceErrors
// and nothing else printed. Only a real invocation showed it.
func TestValidateWatchFlag_ReportsOnStderr(t *testing.T) {
	for _, tc := range []struct{ name, watch, want string }{
		{"below minimum", "100ms", "below the 1s minimum"},
		{"unparseable", "banana", "not a duration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetGlobalOutputFlags()
			t.Cleanup(resetGlobalOutputFlags)
			globalWatch = tc.watch

			var errBuf bytes.Buffer
			cmd, _, err := rootCmd.Find([]string{"search"})
			if err != nil {
				t.Fatalf("find search: %v", err)
			}
			cmd.SetErr(&errBuf)
			t.Cleanup(func() { cmd.SetErr(nil) })

			if err := validateWatchFlag(cmd); err == nil {
				t.Fatalf("validateWatchFlag accepted --watch %q", tc.watch)
			}
			if !strings.Contains(errBuf.String(), tc.want) {
				t.Errorf("stderr = %q, want it to explain %q — an exit 1 with no output is the bug this guards",
					errBuf.String(), tc.want)
			}
			// Cobra must not print it a second time.
			if !rootCmd.SilenceErrors {
				t.Error("root was left un-silenced; the error would be printed twice")
			}
		})
	}
}

// TestValidateWatchFlag_JSONModeEmitsAnEnvelope keeps the --json stderr
// contract (#64) intact for this failure too.
func TestValidateWatchFlag_JSONModeEmitsAnEnvelope(t *testing.T) {
	resetGlobalOutputFlags()
	t.Cleanup(resetGlobalOutputFlags)
	globalWatch = "100ms"
	globalJSON = true

	var errBuf bytes.Buffer
	cmd, _, err := rootCmd.Find([]string{"search"})
	if err != nil {
		t.Fatalf("find search: %v", err)
	}
	cmd.SetErr(&errBuf)
	t.Cleanup(func() { cmd.SetErr(nil) })

	if err := validateWatchFlag(cmd); err == nil {
		t.Fatal("validateWatchFlag accepted a sub-minimum interval")
	}
	var env map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(errBuf.Bytes()), &env); err != nil {
		t.Fatalf("stderr is not a JSON envelope (%v): %s", err, errBuf.String())
	}
	if _, ok := env["error"]; !ok {
		t.Errorf("envelope = %v, want an error field", env)
	}
}

// TestValidateWatchFlag_PassesValidInput leaves a good invocation alone.
func TestValidateWatchFlag_PassesValidInput(t *testing.T) {
	resetGlobalOutputFlags()
	t.Cleanup(resetGlobalOutputFlags)
	globalWatch = "30s"

	cmd, _, err := rootCmd.Find([]string{"search"})
	if err != nil {
		t.Fatalf("find search: %v", err)
	}
	if err := validateWatchFlag(cmd); err != nil {
		t.Errorf("validateWatchFlag rejected a valid --watch: %v", err)
	}
	if rootCmd.SilenceErrors {
		t.Error("a valid --watch silenced the root; later errors would go unreported")
	}
}
