package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"notioncli/utils"

	"github.com/fatih/color"
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

// TestTeamsListDispatch_SurfacesStubError runs `notioncli teams list`
// and asserts the osExit recorder fires with code 1 (the stub path).
// The purpose is to confirm the CLI wires the stub error cleanly rather
// than panicking.
func TestTeamsListDispatch_SurfacesStubError(t *testing.T) {
	// Env scaffolding: teams list calls utils.SetAPIConfig which needs
	// env vars and a loadable .env.
	emptyCwd := t.TempDir()
	emptyHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(emptyCwd, ".env"), []byte(""), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(emptyCwd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	t.Setenv("HOME", emptyHome)
	t.Setenv("NOTION_API_KEY", "test-key")
	t.Setenv("NOTION_PAGE_ID", "pageID")
	t.Setenv("LOCAL_TIMEZONE", "UTC")

	// Swap osExit so the test binary survives the stub's exit(1).
	var exited bool
	var code int
	origExit := osExit
	osExit = func(c int) {
		exited = true
		code = c
	}
	t.Cleanup(func() { osExit = origExit })

	// Swap fatih/color's package-level writers so color.Red's output is
	// observable. Without this the sentinel error lands on os.Stderr and
	// the #11 assertion below can't see it.
	var out bytes.Buffer
	origColorOut := color.Output
	origColorErr := color.Error
	color.Output = &out
	color.Error = &out
	t.Cleanup(func() {
		color.Output = origColorOut
		color.Error = origColorErr
	})

	resetRootCmdArgs()
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"teams", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute(teams list): %v", err)
	}

	if !exited {
		t.Fatal("teams list did not invoke osExit; expected stub error path")
	}
	if code != 1 {
		t.Errorf("teams list osExit code = %d, want 1", code)
	}

	// The stub error must bubble through the CLI surface so operators
	// who grep for "#11" in the output land on the tracking issue. This
	// also confirms the discarded-team return at cmd/teams.go isn't
	// suppressing the sentinel before the color.Red call.
	if got := out.String(); !strings.Contains(got, "#11") {
		t.Errorf("teams list output missing #11 marker:\n%s", got)
	}
}

// TestTeamsListErrExposedViaUtils is a belt-and-braces check that the
// cmd layer is paired with the utils stub so a future refactor can't
// silently drop the error.
func TestTeamsListErrExposedViaUtils(t *testing.T) {
	if utils.ErrTeamsNotSupported == nil {
		t.Fatal("utils.ErrTeamsNotSupported must be non-nil for teams list to have a stable error")
	}
}
