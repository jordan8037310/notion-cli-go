package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"notioncli/utils"

	"github.com/fatih/color"
)

// TestFiles_Cmd_Registered asserts the four new subcommands (two under
// blocks, two under pages) are wired into rootCmd.
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
func TestFiles_Cmd_AddFileHasNameFlag(t *testing.T) {
	addFile := findBlocksSubcommand(t, "add-file")
	if addFile.Flags().Lookup("name") == nil {
		t.Error("blocks add-file: --name flag not registered")
	}
}

// TestFiles_NewFileClient_Cmd_ReturnsErrMissingAPIKey drives the
// cmd-layer helper directly.
func TestFiles_NewFileClient_Cmd_ReturnsErrMissingAPIKey(t *testing.T) {
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

// runFilesCmd runs a files-related subcommand through rootCmd and
// returns cobra's Execute error plus the color output buffer.
func runFilesCmd(t *testing.T, args []string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	resetRootCmdArgs()
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(args)

	// Capture fatih/color output so the happy-path "Uploaded..." line
	// is visible to assertions.
	origColorOut := color.Output
	origColorErr := color.Error
	color.Output = &buf
	color.Error = &buf
	t.Cleanup(func() {
		color.Output = origColorOut
		color.Error = origColorErr
	})

	err := rootCmd.Execute()
	return buf.String(), err
}

// TestFiles_Cmd_DispatchHappyPath is the end-to-end smoke test for
// every subcommand: each runs the full two-step upload flow against the
// cmd mock server and prints the expected "Uploaded ..." / "Set ..."
// line with the file ID.
func TestFiles_Cmd_DispatchHappyPath(t *testing.T) {
	_ = withCmdEnv(t)

	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "hello.png")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}

	tests := []struct {
		name     string
		args     []string
		wantFrag string
	}{
		{"blocks add-image", []string{"blocks", "add-image", filePath}, "Uploaded image"},
		{"blocks add-file", []string{"blocks", "add-file", filePath}, "Uploaded file"},
		{"blocks add-file with --name", []string{"blocks", "add-file", filePath, "--name", "nice-name.txt"}, "nice-name.txt"},
		{"pages set-icon", []string{"pages", "set-icon", "page-abc", filePath}, "Uploaded icon for page"},
		{"pages set-cover", []string{"pages", "set-cover", "page-abc", filePath}, "Uploaded cover for page"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			// Reset --name flag between rows since it's package-level.
			blocksAddFileName = ""
			out, err := runFilesCmd(t, tt.args)
			if err != nil {
				t.Fatalf("%s: unexpected error: %v (output=%s)", tt.name, err, out)
			}
			if !strings.Contains(out, tt.wantFrag) {
				t.Errorf("%s: output missing %q:\n%s", tt.name, tt.wantFrag, out)
			}
			if !strings.Contains(out, "cmd-file-id") {
				t.Errorf("%s: output missing uploaded file id:\n%s", tt.name, out)
			}
		})
	}
}

// TestFiles_Cmd_Dispatch_PathValidation confirms per-path validation
// errors surface directly (not buried behind an auth error).
func TestFiles_Cmd_Dispatch_PathValidation(t *testing.T) {
	_ = withCmdEnv(t)

	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.bin")
	subdir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tests := []struct {
		name     string
		args     []string
		wantFrag string
	}{
		{"add-image missing path", []string{"blocks", "add-image", missing}, "nope.bin"},
		{"add-file directory", []string{"blocks", "add-file", subdir}, "directory"},
		{"set-icon missing path", []string{"pages", "set-icon", "page-abc", missing}, "nope.bin"},
		{"set-cover directory", []string{"pages", "set-cover", "page-abc", subdir}, "directory"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			blocksAddFileName = ""
			_, err := runFilesCmd(t, tt.args)
			if err == nil {
				t.Fatalf("%s: want error, got nil", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantFrag) {
				t.Errorf("%s: want error to contain %q, got %v", tt.name, tt.wantFrag, err)
			}
		})
	}
}

// TestFiles_Cmd_Dispatch_ArgValidation asserts cobra's positional-arg
// constraints for each command.
func TestFiles_Cmd_Dispatch_ArgValidation(t *testing.T) {
	_ = withCmdEnv(t)

	tests := []struct {
		name string
		args []string
	}{
		{"add-image no args", []string{"blocks", "add-image"}},
		{"add-file no args", []string{"blocks", "add-file"}},
		{"set-icon one arg", []string{"pages", "set-icon", "page-abc"}},
		{"set-cover one arg", []string{"pages", "set-cover", "page-abc"}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			blocksAddFileName = ""
			_, err := runFilesCmd(t, tt.args)
			if err == nil {
				t.Fatalf("%s: want cobra arg error, got nil", tt.name)
			}
		})
	}
}
