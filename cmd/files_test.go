package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"notioncli/utils"
)

// findBlocksSubcommand and findPagesSubcommand are defined in blocks_test.go
// and pages_test.go respectively — this file reuses them without redeclaring.

// TestFiles_Cmd_Registered asserts the four new subcommands (two under
// blocks, two under pages) are wired into rootCmd. The cmd-layer gap check
// looks for Test functions per exported command — this single test covers
// all four to keep the test file flat.
func TestFiles_Cmd_Registered(t *testing.T) {
	wantBlocks := []string{"add-image", "add-file"}
	for _, name := range wantBlocks {
		if findBlocksSubcommand(t, name) == nil {
			t.Errorf("blocks subcommand %q not registered", name)
		}
	}
	wantPages := []string{"set-icon", "set-cover"}
	for _, name := range wantPages {
		if findPagesSubcommand(t, name) == nil {
			t.Errorf("pages subcommand %q not registered", name)
		}
	}
}

// TestFiles_Cmd_AddFileHasNameFlag locks in the --name flag on add-file.
// The flag exists so callers can override the displayed filename without
// renaming the local file; a regression that dropped it would silently
// change behavior.
func TestFiles_Cmd_AddFileHasNameFlag(t *testing.T) {
	addFile := findBlocksSubcommand(t, "add-file")
	if addFile.Flags().Lookup("name") == nil {
		t.Error("blocks add-file: --name flag not registered")
	}
}

// TestFiles_NewFileClient_Cmd_ReturnsErrMissingAPIKey drives the cmd-layer
// helper directly. When NOTION_API_KEY resolves empty the helper must
// surface utils.ErrMissingAPIKey so operators get a configuration-specific
// error (consistent with newPageClient). SetAPIConfig calls os.Exit when
// the env var is unset, so we set it to a placeholder then clear it to
// the empty string — which passes SetAPIConfig but trips the emptiness
// check in newFileClient.
func TestFiles_NewFileClient_Cmd_ReturnsErrMissingAPIKey(t *testing.T) {
	// Env scaffolding: SetAPIConfig needs a loadable .env, HOME, and the
	// two required env vars. We set NOTION_API_KEY to "" explicitly —
	// LookupEnv returns ok=true for empty values, so SetAPIConfig returns
	// ("", pageID) and newFileClient's emptiness check fires.
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
	t.Setenv("NOTION_API_KEY", "")
	t.Setenv("NOTION_PAGE_ID", "pageID")
	t.Setenv("LOCAL_TIMEZONE", "UTC")

	got, err := newFileClient()
	if err == nil {
		t.Fatalf("newFileClient: want error, got %+v", got)
	}
	if !errors.Is(err, utils.ErrMissingAPIKey) {
		t.Errorf("newFileClient: want errors.Is ErrMissingAPIKey, got %v", err)
	}
	if got != nil {
		t.Errorf("newFileClient: want nil client, got %+v", got)
	}
}

// runFilesCmd is a tiny helper that runs a files-related subcommand
// through rootCmd with its full arg list, captures stdout+stderr into buf,
// and returns whatever cobra's Execute returned. RunE errors surface
// through the return, not via cmd.SetErr, so callers assert on both.
func runFilesCmd(t *testing.T, args []string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	resetRootCmdArgs()
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return buf.String(), err
}

// TestFiles_Cmd_DispatchReturnsStub is the end-to-end smoke test for every
// new subcommand. Each row dispatches through rootCmd with valid args
// (including a real on-disk file), the env fully wired, and asserts the
// stub sentinel bubbles up with the right prefix. This ensures the cmd
// layer threads the error cleanly (no silent swallow) and that the RunE
// return value carries #11 so users see it in their terminal.
func TestFiles_Cmd_DispatchReturnsStub(t *testing.T) {
	// Shared env + filesystem setup for every row.
	withCmdEnv(t)

	// A small real file that passes validateUploadPath; 1 byte suffices.
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "hello.png")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}

	tests := []struct {
		name       string
		args       []string
		wantPrefix string
	}{
		{
			name:       "blocks add-image",
			args:       []string{"blocks", "add-image", filePath},
			wantPrefix: "add-image",
		},
		{
			name:       "blocks add-file",
			args:       []string{"blocks", "add-file", filePath},
			wantPrefix: "add-file",
		},
		{
			name:       "blocks add-file with --name",
			args:       []string{"blocks", "add-file", filePath, "--name", "nice-name.txt"},
			wantPrefix: "add-file",
		},
		{
			name:       "pages set-icon",
			args:       []string{"pages", "set-icon", "page-abc", filePath},
			wantPrefix: "set-icon",
		},
		{
			name:       "pages set-cover",
			args:       []string{"pages", "set-cover", "page-abc", filePath},
			wantPrefix: "set-cover",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			_, err := runFilesCmd(t, tt.args)
			if err == nil {
				t.Fatalf("%s: want error, got nil", tt.name)
			}
			if !errors.Is(err, utils.ErrFileUploadNotSupported) {
				t.Errorf("%s: want errors.Is ErrFileUploadNotSupported, got %v", tt.name, err)
			}
			if !strings.HasPrefix(err.Error(), tt.wantPrefix+":") {
				t.Errorf("%s: want error to start with %q, got %v", tt.name, tt.wantPrefix+":", err)
			}
			if !strings.Contains(err.Error(), "#11") {
				t.Errorf("%s: want error to mention #11, got %v", tt.name, err)
			}
		})
	}
}

// TestFiles_Cmd_Dispatch_PathValidation confirms the cmd layer surfaces
// per-path validation errors (missing file, directory, oversize) rather
// than the stub sentinel. Operators who typo a path need the specific
// error, not the "will be enabled by #11" noise.
func TestFiles_Cmd_Dispatch_PathValidation(t *testing.T) {
	withCmdEnv(t)

	dir := t.TempDir()
	// Missing path.
	missing := filepath.Join(dir, "nope.bin")
	// Directory path.
	subdir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tests := []struct {
		name     string
		args     []string
		wantFrag string
	}{
		{
			name:     "add-image missing path",
			args:     []string{"blocks", "add-image", missing},
			wantFrag: "nope.bin",
		},
		{
			name:     "add-file directory",
			args:     []string{"blocks", "add-file", subdir},
			wantFrag: "directory",
		},
		{
			name:     "set-icon missing path",
			args:     []string{"pages", "set-icon", "page-abc", missing},
			wantFrag: "nope.bin",
		},
		{
			name:     "set-cover directory",
			args:     []string{"pages", "set-cover", "page-abc", subdir},
			wantFrag: "directory",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			_, err := runFilesCmd(t, tt.args)
			if err == nil {
				t.Fatalf("%s: want error, got nil", tt.name)
			}
			// Validation errors must not be wrapped as the stub
			// sentinel — they are a separate failure class.
			if errors.Is(err, utils.ErrFileUploadNotSupported) {
				t.Errorf("%s: validation error should not wrap ErrFileUploadNotSupported, got %v", tt.name, err)
			}
			if !strings.Contains(err.Error(), tt.wantFrag) {
				t.Errorf("%s: want error to contain %q, got %v", tt.name, tt.wantFrag, err)
			}
		})
	}
}

// TestFiles_Cmd_Dispatch_ArgValidation asserts cobra's positional-arg
// constraints for each command. add-image/add-file require exactly one
// arg, set-icon/set-cover require exactly two. A missing path must return
// a cobra usage error, not run the stub.
func TestFiles_Cmd_Dispatch_ArgValidation(t *testing.T) {
	withCmdEnv(t)

	tests := []struct {
		name string
		args []string
	}{
		{name: "add-image no args", args: []string{"blocks", "add-image"}},
		{name: "add-file no args", args: []string{"blocks", "add-file"}},
		{name: "set-icon one arg", args: []string{"pages", "set-icon", "page-abc"}},
		{name: "set-cover one arg", args: []string{"pages", "set-cover", "page-abc"}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			// Silence cobra's usage dump by pointing the command
			// output at a throwaway buffer; we only care about
			// the returned error.
			_, err := runFilesCmd(t, tt.args)
			if err == nil {
				t.Fatalf("%s: want cobra arg error, got nil", tt.name)
			}
		})
	}
}
