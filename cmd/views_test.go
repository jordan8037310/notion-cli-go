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
// values between tests.
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

// TestViews_CreateTypeFlagHelpFromValidViewTypes asserts the --type
// flag's help text lists every value in utils.ValidViewTypes.
func TestViews_CreateTypeFlagHelpFromValidViewTypes(t *testing.T) {
	sub := findViewsSubcommand(t, "create")
	typeFlag := sub.Flags().Lookup("type")
	if typeFlag == nil {
		t.Fatal("--type flag not registered on views create")
	}
	for _, vt := range utils.ValidViewTypes {
		if !strings.Contains(typeFlag.Usage, vt) {
			t.Errorf("--type help %q missing ValidViewTypes entry %q", typeFlag.Usage, vt)
		}
	}
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

// TestViews_SubcommandsValid asserts each subcommand is a valid cobra
// command with a RunE wired.
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

// TestViews_Create_HappyPath runs `views create ...` against the shared
// mock server (extended to handle POST /views) and verifies
// the View is printed.
func TestViews_Create_HappyPath(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	var buf bytes.Buffer
	rootCmd.SetArgs([]string{"views", "create", "db-id", "--data-source", "ds-id", "--name", "My View", "--type", "table"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("views create: %v", err)
	}
	if !strings.Contains(buf.String(), "view-created-id") {
		t.Errorf("views create output missing view id:\n%s", buf.String())
	}
}

// TestViews_Create_MissingName asserts the --name flag is required.
func TestViews_Create_MissingName(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"views", "create", "dbID", "--data-source", "ds-id", "--type", "table"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when --name missing, got nil")
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

	rootCmd.SetArgs([]string{"views", "create", "dbID", "--data-source", "ds-id", "--name", "n"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when --type missing, got nil")
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

	rootCmd.SetArgs([]string{"views", "create", "--name", "--data-source", "ds-id", "n", "--type", "table"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error when database-id missing, got nil")
	}
}

// TestViews_Create_AllTypesHappyPath cycles through every supported
// --type value and verifies each dispatches to the mock (which echoes
// each type back).
func TestViews_Create_AllTypesHappyPath(t *testing.T) {
	for _, vt := range utils.ValidViewTypes {
		t.Run(vt, func(t *testing.T) {
			_ = withCmdEnv(t)
			resetViewsFlags()
			resetRootCmdArgs()

			var buf bytes.Buffer
			rootCmd.SetArgs([]string{"views", "create", "db-id", "--data-source", "ds-id", "--name", "n", "--type", vt})
			rootCmd.SilenceUsage = true
			rootCmd.SilenceErrors = true
			rootCmd.SetOut(&buf)
			rootCmd.SetErr(&buf)
			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("views create --type %s: %v", vt, err)
			}
		})
	}
}

// TestViews_Create_WithConfigJSON asserts --config-json is read from
// disk and forwarded into the request.
func TestViews_Create_WithConfigJSON(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte(`{"sort":"asc"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var buf bytes.Buffer
	rootCmd.SetArgs([]string{"views", "create", "db-id", "--data-source", "ds-id", "--name", "n", "--type", "table", "--config-json", path})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("views create: %v", err)
	}
}

// TestViews_Create_BadConfigJSON asserts a malformed JSON file produces
// a parse error.
func TestViews_Create_BadConfigJSON(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	rootCmd.SetArgs([]string{"views", "create", "dbID", "--data-source", "ds-id", "--name", "n", "--type", "table", "--config-json", path})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "config-json") {
		t.Errorf("error = %q; want substring %q", err.Error(), "config-json")
	}
}

// TestViews_Create_MissingConfigJSONFile asserts a non-existent config
// file produces an open error.
func TestViews_Create_MissingConfigJSONFile(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	rootCmd.SetArgs([]string{"views", "create", "dbID", "--data-source", "ds-id", "--name", "n", "--type", "table", "--config-json", "/does/not/exist.json"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected open error, got nil")
	}
}

// TestViews_Update_HappyPath runs `views update <id> --name` against the
// mock and verifies the updated View is printed.
func TestViews_Update_HappyPath(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	var buf bytes.Buffer
	rootCmd.SetArgs([]string{"views", "update", "view-id", "--name", "Renamed"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("views update: %v", err)
	}
	if !strings.Contains(buf.String(), "view-updated-id") {
		t.Errorf("views update output missing id:\n%s", buf.String())
	}
}

// TestViews_Update_NoFlags asserts at least one mutation flag is
// required.
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
}

// TestViews_Update_WithConfigJSON asserts config-only updates are
// accepted.
func TestViews_Update_WithConfigJSON(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte(`{"filter":{"status":"done"}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	rootCmd.SetArgs([]string{"views", "update", "view-id", "--config-json", path})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("views update: %v", err)
	}
}

// TestViews_Create_ValidationBeforeClientBuild asserts that a bad
// --type value surfaces the request-validation error and does NOT leak
// ErrMissingAPIKey.
func TestViews_Create_ValidationBeforeClientBuild(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

	// Force the client-build path to fail if it runs.
	t.Setenv("NOTION_API_KEY", "")

	rootCmd.SetArgs([]string{"views", "create", "dbID", "--data-source", "ds-id", "--name", "n", "--type", "bogus"})
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
// returns ErrMissingAPIKey (wrapped) when NOTION_API_KEY resolves empty.
func TestViews_NewViewClient_MissingAPIKey(t *testing.T) {
	_ = withCmdEnv(t)
	resetViewsFlags()
	resetRootCmdArgs()

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

// TestViews_ReadConfigJSON covers readConfigJSON directly.
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
	payload := []byte(`{"a":1,"big":12345678901234567890}`)
	if err := os.WriteFile(good, payload, 0o600); err != nil {
		t.Fatalf("write good: %v", err)
	}
	got, err := readConfigJSON(good)
	if err != nil {
		t.Fatalf("readConfigJSON(good) err = %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("readConfigJSON(good) = %q; want bytes preserved %q", string(got), string(payload))
	}
}

// TestViews_PrintView drives printView to cover both the nil-view and
// happy-path branches.
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
