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

// TestTeamsListDispatch_StubReturnsTypedError pins the post-#37
// contract: `notioncli teams list` returns a clear error pointing at
// the upstream API status (Notion-Version 2026-03-11 has no working
// /v1/teams endpoint) rather than letting the live API 400 surface
// raw. Once Notion exposes a teams endpoint we restore the network
// path and this test should flip back to asserting the happy-path
// rendering.
func TestTeamsListDispatch_StubReturnsTypedError(t *testing.T) {
	_ = withCmdEnv(t)

	var buf bytes.Buffer
	resetRootCmdArgs()
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"teams", "list"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("teams list should return an error while the API is stubbed; got nil")
	}
	if !strings.Contains(err.Error(), "teams API unavailable") {
		t.Errorf("teams list error %q should mention 'teams API unavailable' so users see the cause clearly", err.Error())
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
