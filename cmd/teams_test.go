package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// findTeamsSubcommand walks the teams command's children and returns the
// child matching name, or fails the test.
func findTeamsSubcommand(t *testing.T, name string) *cobra.Command {
	t.Helper()
	teamsC := findTopLevelCmd(t, "teams")
	for _, c := range teamsC.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("teams subcommand %q not found", name)
	return nil
}

// TestTeamsCmdRegistered verifies `teams` is a top-level command with a
// list subcommand.
func TestTeamsCmdRegistered(t *testing.T) {
	teamsC := findTopLevelCmd(t, "teams")
	found := false
	for _, c := range teamsC.Commands() {
		if c.Name() == "list" {
			found = true
		}
	}
	if !found {
		t.Error("teams list subcommand not registered")
	}
}

// TestTeamsListExists asserts the list subcommand exists and is a valid
// cobra.Command (catches nil-pointer registration bugs).
func TestTeamsListExists(t *testing.T) {
	list := findTeamsSubcommand(t, "list")
	if list.Use == "" {
		t.Error("teams list: Use field is empty")
	}
}

// TestTeamsListDispatch_HappyPath runs `notioncli teams list` against a
// mock server and asserts the output lists each team (id + name) in the
// expected order. No osExit call on the happy path.
func TestTeamsListDispatch_HappyPath(t *testing.T) {
	_ = withCmdEnv(t)

	// Ensure osExit doesn't fire on the happy path — swap a recorder.
	var exited bool
	origExit := osExit
	osExit = func(c int) { exited = true }
	t.Cleanup(func() { osExit = origExit })

	var buf bytes.Buffer
	resetRootCmdArgs()
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"teams", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(teams list): %v", err)
	}
	if exited {
		t.Errorf("happy path invoked osExit unexpectedly")
	}

	out := buf.String()
	for _, frag := range []string{"team-1", "Marketing"} {
		if !strings.Contains(out, frag) {
			t.Errorf("teams list output missing %q:\n%s", frag, out)
		}
	}
}

// TestTeamsListDispatch_AuthError asserts that an unconfigured API key
// surfaces as a returned error from the RunE handler. The command
// migrated from Run (which called osExit) to RunE (which returns the
// error) as part of the --json rollout, so this test now asserts on
// the returned error rather than osExit.
func TestTeamsListDispatch_AuthError(t *testing.T) {
	_ = withCmdEnv(t)
	// Blank out the API key AFTER withCmdEnv so SetAPIConfig returns an
	// empty string; TeamClient.List then surfaces ErrMissingAPIKey.
	t.Setenv("NOTION_API_KEY", "")

	var out bytes.Buffer
	resetRootCmdArgs()
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"teams", "list"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("teams list with empty API key should return an error")
	}
	if !strings.Contains(err.Error(), "api key") {
		t.Errorf("teams list error missing api-key context: %v", err)
	}
}
