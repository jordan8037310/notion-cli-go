package utils

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewFileClient is a smoke test for the constructor so the gap-check
// script sees a matching Test function for NewFileClient. It also pins the
// behavior that the wrapper stores the caller's *Client verbatim (no copy,
// no indirection), which matters for tests that construct one client and
// reuse it across resource clients.
//
// Name intentionally uses TestNewFileClient (no _Files infix) so
// scripts/check-test-coverage.sh matches Test<FuncName>. TestFiles_* is
// the prefix for every other test in this file per the agent brief.
func TestNewFileClient(t *testing.T) {
	c := NewClient("k", WithBaseURL("http://example"))
	got := NewFileClient(c)
	if got == nil {
		t.Fatal("NewFileClient: got nil")
	}
	if got.c != c {
		t.Fatalf("NewFileClient: c = %p, want %p", got.c, c)
	}
}

// TestNewFileRef pins the constructor's contract: Type is always the
// FileRefTypeFileUpload constant, ID and Name are passed through verbatim,
// and ExpiryTime is empty for caller assignment. A test here means a change
// to the "file_upload" literal has to break either the constant or the
// struct tag — it cannot silently drift.
//
// Name intentionally uses TestNewFileRef (no _Files infix) so
// scripts/check-test-coverage.sh matches Test<FuncName>.
func TestNewFileRef(t *testing.T) {
	got := NewFileRef("abc-123", "hello.png")
	if got == nil {
		t.Fatal("NewFileRef: got nil")
	}
	if got.ID != "abc-123" {
		t.Errorf("NewFileRef: ID = %q, want %q", got.ID, "abc-123")
	}
	if got.Name != "hello.png" {
		t.Errorf("NewFileRef: Name = %q, want %q", got.Name, "hello.png")
	}
	if got.Type != FileRefTypeFileUpload {
		t.Errorf("NewFileRef: Type = %q, want %q", got.Type, FileRefTypeFileUpload)
	}
	if got.Type != "file_upload" {
		t.Errorf("NewFileRef: Type = %q, want the exact Notion discriminator %q", got.Type, "file_upload")
	}
	if got.ExpiryTime != "" {
		t.Errorf("NewFileRef: ExpiryTime = %q, want empty", got.ExpiryTime)
	}
}

// TestFiles_ErrFileUploadNotSupported_MessageReferences11 asserts the error
// message mentions the pinned version and issue #11, so an operator reading
// a CLI error knows which tracking issue to watch. Mirrors the teams
// pattern exactly — if these drift, grepping for "#11" across error
// messages will lose a hit and the PR that removes it gets flagged.
func TestFiles_ErrFileUploadNotSupported_MessageReferences11(t *testing.T) {
	msg := ErrFileUploadNotSupported.Error()
	if !strings.Contains(msg, NotionAPIVersion) {
		t.Errorf("ErrFileUploadNotSupported message = %q; want it to mention %q", msg, NotionAPIVersion)
	}
	if !strings.Contains(msg, "#11") {
		t.Errorf("ErrFileUploadNotSupported message = %q; want it to mention %q", msg, "#11")
	}
}

// writeTempFile creates a file of the requested size under t.TempDir and
// returns the absolute path. Size is exact — the file is filled with a
// repeating byte so callers can round-trip a content assertion without
// parsing.
func writeTempFile(t *testing.T, size int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "upload.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create tempfile: %v", err)
	}
	defer f.Close()
	if size > 0 {
		buf := make([]byte, size)
		for i := range buf {
			buf[i] = byte('a')
		}
		if _, err := f.Write(buf); err != nil {
			t.Fatalf("write tempfile: %v", err)
		}
	}
	return path
}

// TestUpload_NotSupported is the headline assertion for the stub: a
// correctly-validated path still returns ErrFileUploadNotSupported, and the
// returned FileRef is nil. Uses a tiny real file so the size-cap branch is
// NOT exercised here — that branch has its own test below.
//
// Name intentionally starts with TestUpload so the gap checker matches
// FileClient.Upload via Test<FuncName>. Remaining upload tests in this
// file use the TestFiles_Upload_* prefix.
func TestUpload_NotSupported(t *testing.T) {
	client := NewFileClient(NewClient("k", WithBaseURL("http://127.0.0.1:0")))
	path := writeTempFile(t, 16)
	got, err := client.Upload(context.Background(), path)
	if err == nil {
		t.Fatalf("Upload: want error, got FileRef %+v", got)
	}
	if !errors.Is(err, ErrFileUploadNotSupported) {
		t.Errorf("Upload: want errors.Is ErrFileUploadNotSupported, got %v", err)
	}
	if got != nil {
		t.Errorf("Upload: want nil FileRef, got %+v", got)
	}
	if !strings.Contains(err.Error(), "upload.bin") {
		t.Errorf("Upload: want error to name the file, got %v", err)
	}
}

// TestFiles_Upload_MissingAPIKey asserts an unconfigured Client is caught
// BEFORE path validation, matching PageClient.checkAuth's posture. A missing
// key is an operator error; surfacing it through ErrMissingAPIKey lets the
// CLI show a configuration-specific message instead of a generic 401.
func TestFiles_Upload_MissingAPIKey(t *testing.T) {
	client := NewFileClient(NewClient("", WithBaseURL("http://127.0.0.1:0")))
	// A real file is fine — we expect the auth check to short-circuit.
	path := writeTempFile(t, 1)
	got, err := client.Upload(context.Background(), path)
	if err == nil {
		t.Fatalf("Upload: want error, got %+v", got)
	}
	if !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("Upload: want errors.Is ErrMissingAPIKey, got %v", err)
	}
	if got != nil {
		t.Errorf("Upload: want nil FileRef, got %+v", got)
	}
}

// TestFiles_Upload_ValidationErrors table-drives every client-side rejection
// path (empty, missing, directory, oversize). Each row asserts the error
// mentions the path or the cap so operators can action the message.
func TestFiles_Upload_ValidationErrors(t *testing.T) {
	// Prepare fixtures once, reuse across rows.
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.bin")
	// A directory path.
	subdir := filepath.Join(dir, "a-dir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// An oversize file.
	bigPath := filepath.Join(dir, "big.bin")
	big, err := os.Create(bigPath)
	if err != nil {
		t.Fatalf("create big: %v", err)
	}
	if err := big.Truncate(MaxFileUploadBytes + 1); err != nil {
		t.Fatalf("truncate big: %v", err)
	}
	_ = big.Close()

	tests := []struct {
		name          string
		path          string
		wantFragments []string
	}{
		{
			name:          "empty path",
			path:          "",
			wantFragments: []string{"path is required"},
		},
		{
			name:          "missing file",
			path:          missing,
			wantFragments: []string{"stat", "does-not-exist.bin"},
		},
		{
			name:          "directory",
			path:          subdir,
			wantFragments: []string{"a-dir", "directory"},
		},
		{
			name:          "oversize file",
			path:          bigPath,
			wantFragments: []string{"big.bin", "exceeds client cap"},
		},
	}

	client := NewFileClient(NewClient("k", WithBaseURL("http://127.0.0.1:0")))
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := client.Upload(context.Background(), tt.path)
			if err == nil {
				t.Fatalf("Upload(%q): want error, got %+v", tt.path, got)
			}
			if got != nil {
				t.Errorf("Upload(%q): want nil FileRef, got %+v", tt.path, got)
			}
			// Validation errors must NOT be wrapped as
			// ErrFileUploadNotSupported — operators need to see the
			// specific path problem, not the version-bump stub error.
			if errors.Is(err, ErrFileUploadNotSupported) {
				t.Errorf("Upload(%q): validation error should not wrap ErrFileUploadNotSupported, got %v", tt.path, err)
			}
			msg := err.Error()
			for _, frag := range tt.wantFragments {
				if !strings.Contains(msg, frag) {
					t.Errorf("Upload(%q): error %q missing fragment %q", tt.path, msg, frag)
				}
			}
		})
	}
}

// TestFiles_Upload_RespectsCtxCancel asserts the stub returns a
// context-cancellation error rather than the stub sentinel when the caller
// has already cancelled the ctx. The real #11 implementation will thread
// ctx into both HTTP calls; locking the cancel-first behavior here means a
// switchover that loses the ctx.Err() check fails this test.
func TestFiles_Upload_RespectsCtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := NewFileClient(NewClient("k", WithBaseURL("http://127.0.0.1:0")))
	path := writeTempFile(t, 8)
	got, err := client.Upload(ctx, path)
	if err == nil {
		t.Fatalf("Upload: want ctx.Canceled error, got %+v", got)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Upload: want errors.Is context.Canceled, got %v", err)
	}
	if errors.Is(err, ErrFileUploadNotSupported) {
		t.Errorf("Upload: cancelled ctx should not surface ErrFileUploadNotSupported, got %v", err)
	}
	if got != nil {
		t.Errorf("Upload: want nil FileRef, got %+v", got)
	}
}

// TestFiles_Upload_ExactlyAtSizeCap verifies the boundary between the
// accepted and rejected branches. A file of exactly MaxFileUploadBytes must
// pass validation (and therefore surface the stub error, not a size error).
// This locks in the ">= vs >" choice so a later refactor cannot drift it
// without a failing test.
func TestFiles_Upload_ExactlyAtSizeCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "at-cap.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := f.Truncate(MaxFileUploadBytes); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	_ = f.Close()

	client := NewFileClient(NewClient("k", WithBaseURL("http://127.0.0.1:0")))
	_, err = client.Upload(context.Background(), path)
	if err == nil {
		t.Fatal("Upload: want stub error, got nil")
	}
	if !errors.Is(err, ErrFileUploadNotSupported) {
		t.Errorf("Upload at exact cap: want errors.Is ErrFileUploadNotSupported, got %v", err)
	}
}
