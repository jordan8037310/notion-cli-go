package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"notioncli/utils"

	"github.com/spf13/cobra"
)

// resetViewsFlags restores package-level view flag vars to their zero
// values between tests. cobra persists flag state on the shared command
// instances, so tests that share rootCmd must reset between runs.
func resetViewsFlags() {
	viewsCreateName = ""
	viewsCreateType = ""
	viewsCreateConfigFile = ""
	viewsUpdateName = ""
	viewsUpdateConfigFile = ""
}

// findViewsSubcommand walks the views command's children and returns
// the child matching name, or fails the test.
func findViewsSubcommand(t *testing.T, name string) *cobra.Command {
	t.Helper()
	viewsC := findTopLevelCmd(t, "views")
	for _, c := range viewsC.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("views subcommand %q not found", name)
	return nil
}

// TestViews_CmdRegistered verifies `views` is a top-level command with
// `create` and `update` subcommands.
func TestViews_CmdRegistered(t *testing.T) {
	viewsC := findTopLevelCmd(t, "views")
	want := map[string]bool{"create": false, "update": false}
	for _, c := range viewsC.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("views subcommand %q not registered", name)
		}
	}
}

// TestViews_SubcommandsValid asserts each subcommand is a non-empty
// *cobra.Command (catches nil-pointer registration bugs).
func TestViews_SubcommandsValid(t *testing.T) {
	for _, name := range []string{"create", "update"} {
		sub := findViewsSubcommand(t, name)
		if sub.Use == "" {
			t.Errorf("views %s: Use field is empty", name)
		}
		if sub.RunE == nil {
			t.Errorf("views %s: RunE is nil (expected for proper exit-code propagation)", name)
		}
	}
}

// TestViews_Create_DispatchSurfacesStub runs `views create ...` and
// asserts that rootCmd.Execute returns a non-nil error — proving the
// stub's ErrViewsNotSupported propagates up through cobra so shell
// callers see a non-zero exit code.
func TestViews_Create_DispatchSurfacesStub(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"views", "create", "dbID", "--name", "My View", "--type", "table"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("views create: expected non-nil error from stub, got nil")
	}
	if !errors.Is(err, utils.ErrViewsNotSupported) {
		t.Errorf("views create: want errors.Is ErrViewsNotSupported, got %v", err)
	}
}

// TestViews_Create_MissingName asserts the --name flag is required and
// that RunE returns a non-nil error (before ever touching the stub).
func TestViews_Create_MissingName(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"views", "create", "dbID", "--type", "table"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when --name missing, got nil")
	}
	if errors.Is(err, utils.ErrViewsNotSupported) {
		t.Errorf("expected flag-validation error, got stub sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), "--name") {
		t.Errorf("error = %q; want substring %q", err.Error(), "--name")
	}
}

// TestViews_Create_MissingType asserts the --type flag is required.
func TestViews_Create_MissingType(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"views", "create", "dbID", "--name", "n"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when --type missing, got nil")
	}
	if errors.Is(err, utils.ErrViewsNotSupported) {
		t.Errorf("expected flag-validation error, got stub sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), "--type") {
		t.Errorf("error = %q; want substring %q", err.Error(), "--type")
	}
}

// TestViews_Create_WrongArgCount asserts cobra enforces exactly one
// positional argument (the database-id).
func TestViews_Create_WrongArgCount(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"views", "create", "--name", "n", "--type", "table"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error when database-id missing, got nil")
	}
}

// TestViews_Create_AllTypesDispatchStub cycles through every supported
// --type value and verifies each dispatches to the stub (vs being
// rejected as invalid at the CLI layer). Guards against silently
// dropping a supported type during refactors.
func TestViews_Create_AllTypesDispatchStub(t *testing.T) {
	for _, vt := range utils.ValidViewTypes {
		t.Run(vt, func(t *testing.T) {
			_ = withCmdEnv(t)
			resetViewsFlags()
			resetRootCmdArgs()

			rootCmd.SetArgs([]string{"views", "create", "dbID", "--name", "n", "--type", vt})
			rootCmd.SilenceUsage = true
			rootCmd.SilenceErrors = true
			err := rootCmd.Execute()
			if err == nil {
				t.Fatalf("views create --type %s: expected error, got nil", vt)
			}
			if !errors.Is(err, utils.ErrViewsNotSupported) {
				t.Errorf("views create --type %s: want ErrViewsNotSupported, got %v", vt, err)
			}
		})
	}
}

// TestViews_Create_WithConfigJSON asserts --config-json is read from
// disk and forwarded into the request (still dispatching to the stub
// today).
func TestViews_Create_WithConfigJSON(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte(`{"sort":"asc"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	rootCmd.SetArgs([]string{"views", "create", "dbID", "--name", "n", "--type", "table", "--config-json", path})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected stub error, got nil")
	}
	if !errors.Is(err, utils.ErrViewsNotSupported) {
		t.Errorf("want ErrViewsNotSupported, got %v", err)
	}
}

// TestViews_Create_BadConfigJSON asserts a malformed JSON file produces
// a parse error ahead of the stub.
func TestViews_Create_BadConfigJSON(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	rootCmd.SetArgs([]string{"views", "create", "dbID", "--name", "n", "--type", "table", "--config-json", path})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if errors.Is(err, utils.ErrViewsNotSupported) {
		t.Errorf("parse error swallowed by stub: %v", err)
	}
	if !strings.Contains(err.Error(), "config-json") {
		t.Errorf("error = %q; want substring %q", err.Error(), "config-json")
	}
}

// TestViews_Create_MissingConfigJSONFile asserts a non-existent config
// file produces an open error ahead of the stub.
func TestViews_Create_MissingConfigJSONFile(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"views", "create", "dbID", "--name", "n", "--type", "table", "--config-json", "/does/not/exist.json"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected open error, got nil")
	}
	if errors.Is(err, utils.ErrViewsNotSupported) {
		t.Errorf("open error swallowed by stub: %v", err)
	}
}

// TestViews_Update_DispatchSurfacesStub runs `views update <id> --name`
// and asserts the stub error propagates.
func TestViews_Update_DispatchSurfacesStub(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"views", "update", "viewID", "--name", "Renamed"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("views update: expected non-nil error from stub, got nil")
	}
	if !errors.Is(err, utils.ErrViewsNotSupported) {
		t.Errorf("views update: want errors.Is ErrViewsNotSupported, got %v", err)
	}
}

// TestViews_Update_NoFlags asserts at least one mutation flag is
// required. The error must precede the stub.
func TestViews_Update_NoFlags(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"views", "update", "viewID"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no mutation flags, got nil")
	}
	if errors.Is(err, utils.ErrViewsNotSupported) {
		t.Errorf("expected flag-validation error, got stub sentinel: %v", err)
	}
}

// TestViews_Update_WithConfigJSON asserts config-only updates are
// accepted and dispatched to the stub.
func TestViews_Update_WithConfigJSON(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte(`{"filter":{"status":"done"}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	rootCmd.SetArgs([]string{"views", "update", "viewID", "--config-json", path})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected stub error, got nil")
	}
	if !errors.Is(err, utils.ErrViewsNotSupported) {
		t.Errorf("want ErrViewsNotSupported, got %v", err)
	}
}

// TestViews_Create_ValidationBeforeClientBuild asserts that a bad
// --type value surfaces the request-validation error and does NOT leak
// the ErrMissingAPIKey that newViewClient would otherwise return when
// the env is blank. Ordering: validate request first, then build client.
func TestViews_Create_ValidationBeforeClientBuild(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	// Force the client-build path to fail if it runs, so we can prove
	// validation ran first by observing the precise validation error
	// instead of ErrMissingAPIKey.
	t.Setenv("NOTION_API_KEY", "")

	rootCmd.SetArgs([]string{"views", "create", "dbID", "--name", "n", "--type", "bogus"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if errors.Is(err, utils.ErrMissingAPIKey) {
		t.Errorf("client build ran before validation: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid type") {
		t.Errorf("error = %q; want substring %q", err.Error(), "invalid type")
	}
}

// TestViews_NewViewClient_MissingAPIKey asserts that newViewClient
// returns ErrMissingAPIKey (wrapped) when NOTION_API_KEY resolves empty
// rather than silently building a Client that would later 401 on the
// real #11 implementation.
func TestViews_NewViewClient_MissingAPIKey(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	// Blank out the API key after the usual cmd env has been set up.
	// withCmdEnv's .env file is empty so the Setenv below wins.
	t.Setenv("NOTION_API_KEY", "")

	vc, err := newViewClient()
	if err == nil {
		t.Fatal("expected error when NOTION_API_KEY empty, got nil")
	}
	if vc != nil {
		t.Errorf("expected nil client on error, got %+v", vc)
	}
	if !errors.Is(err, utils.ErrMissingAPIKey) {
		t.Errorf("expected errors.Is ErrMissingAPIKey, got %v", err)
	}
}

// TestViews_ErrExposedViaUtils is a belt-and-braces check that the cmd
// layer is paired with the utils stub so a future refactor can't
// silently drop the error.
func TestViews_ErrExposedViaUtils(t *testing.T) {
	if utils.ErrViewsNotSupported == nil {
		t.Fatal("utils.ErrViewsNotSupported must be non-nil for views commands to surface a stable error")
	}
}

// TestViews_ReadConfigJSON covers readConfigJSON directly so the helper
// has coverage independent of the RunE paths. Exercises the empty-path
// shortcut and the empty-file rejection.
func TestViews_ReadConfigJSON(t *testing.T) {
	if got, err := readConfigJSON(""); err != nil || got != nil {
		t.Errorf("readConfigJSON(\"\") = (%v, %v); want (nil, nil)", got, err)
	}

	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte{}, 0o600); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	if got, err := readConfigJSON(empty); err == nil {
		t.Errorf("readConfigJSON(empty) = (%v, nil); want error", got)
	}

	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatalf("write good: %v", err)
	}
	got, err := readConfigJSON(good)
	if err != nil {
		t.Fatalf("readConfigJSON(good) err = %v", err)
	}
	if got["a"] != float64(1) { // JSON numbers decode to float64
		t.Errorf("readConfigJSON(good)[a] = %v, want 1", got["a"])
	}
}

// TestViews_PrintView drives printView to cover both the nil-view and
// happy-path branches. Writes into a bytes.Buffer so output assertions
// are deterministic.
func TestViews_PrintView(t *testing.T) {
	var buf bytes.Buffer
	printView(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("printView(nil) wrote %q; want empty", buf.String())
	}

	buf.Reset()
	printView(&buf, &utils.View{Object: "view", ID: "v1", Name: "My View", Type: "table"})
	out := buf.String()
	if !strings.Contains(out, "v1") || !strings.Contains(out, "table") {
		t.Errorf("printView JSON missing expected fields:\n%s", out)
	}
}
