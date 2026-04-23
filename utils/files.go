// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrFileUploadNotSupported is returned by FileClient methods when the pinned
// Notion API version does not expose the file-upload endpoints. The
// /v1/file_uploads surface was introduced in a Notion-Version newer than the
// 2022-06-28 value this CLI currently pins (the Notion docs for
// POST /v1/file_uploads require at least 2026-03-11). Issue #11 tracks the
// version bump that will enable the real two-step upload flow; until then
// this client surfaces a typed sentinel so callers can check for it with
// errors.Is.
var ErrFileUploadNotSupported = errors.New("file uploads are not supported on Notion-Version " + NotionAPIVersion + "; will be enabled by issue #11")

// MaxFileUploadBytes is the client-side cap applied before we even contact
// Notion. Notion caps single-part uploads at 20MB (multi-part is larger), so
// 20MB is a conservative default that keeps the single-part flow simple for
// v1. Larger files will be rejected with a clear error referencing this cap.
//
// This constant lives alongside the stub deliberately — issue #11's real
// implementation will honor the same size gate so callers that branch on it
// keep working when the stub flips to a real upload.
const MaxFileUploadBytes int64 = 20 * 1024 * 1024

// FileRefTypeFileUpload is the Notion "type" discriminator for a file
// reference created through /v1/file_uploads. Exposed as a constant so block
// and page payload builders spell the literal exactly once — prevents typo
// drift (e.g. "file-upload", "fileupload") that a string-typed field would
// otherwise allow.
const FileRefTypeFileUpload = "file_upload"

// FileRef is the shape returned by a successful upload. Type is always
// FileRefTypeFileUpload for files created via /v1/file_uploads — the
// constant lets callers feed a FileRef directly into block create / page
// icon / page cover payloads without caring where the reference came from.
//
// Name and ExpiryTime are populated when Notion returns them. ExpiryTime is
// an RFC3339 timestamp (per the Notion API) kept as a string so the envelope
// round-trips unchanged even if Notion adds timezone or precision variants
// in a future version.
//
// Prefer NewFileRef for construction so Type is pinned to the constant.
type FileRef struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Name       string `json:"name,omitempty"`
	ExpiryTime string `json:"expiry_time,omitempty"`
}

// NewFileRef builds a FileRef with Type pinned to FileRefTypeFileUpload. The
// real upload implementation (#11) should prefer this constructor over
// struct-literal construction so the discriminator cannot drift. ExpiryTime
// is set separately by the caller when the upload response carries it.
func NewFileRef(id, name string) *FileRef {
	return &FileRef{
		ID:   id,
		Type: FileRefTypeFileUpload,
		Name: name,
	}
}

// FileUploadRequest is the body for POST /v1/file_uploads. Mode selects the
// upload style — "single_part" is the v1 default and the only mode the stub
// will exercise when issue #11 flips the implementation. Filename is the
// human-readable name shown in Notion after the upload completes.
//
// TODO(#11): multi-part uploads. Per the Notion 2026-03-11 docs for
// POST /v1/file_uploads, switching Mode to "multi_part" requires these
// additional fields on the request body:
//
//   - number_of_parts (int) — total parts the caller will PUT; required and
//     must be >= 2 when Mode == "multi_part".
//   - part_number (int) — 1-indexed part identifier supplied on each PUT to
//     the pre-signed UploadURL (not on the initial POST — kept here for
//     symmetry so #11's implementer can add it alongside number_of_parts and
//     decide whether to model a separate PartUploadRequest struct).
//   - external_url (string, optional) — mutually exclusive with multipart
//     bytes; used for "upload by URL" mode which is a third variant beyond
//     single_part / multi_part.
//
// The fields are documented rather than declared so a zero-value
// FileUploadRequest still serializes cleanly on the pinned version. Issue
// #11 should add them as exported fields with `omitempty` tags so the
// single-part path's JSON shape stays unchanged.
type FileUploadRequest struct {
	Mode        string `json:"mode,omitempty"`
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

// FileUploadResponse is the envelope Notion returns from
// POST /v1/file_uploads. The stub defines the shape so callers can start
// consuming it today and the real implementation is a drop-in.
//
// UploadURL is the pre-signed URL to which raw bytes are PUT as
// multipart/form-data in step 2. It is separate from ID because the URL
// expires once the upload completes, while ID continues to reference the
// uploaded file.
//
// Filename is what Notion echoes back from the request; FileRef.Name is the
// canonical display name callers hydrate into their block/page payloads.
// #11's implementer should treat FileRef.Name as canonical (feed it from
// filepath.Base(path) or the caller's --name override) and use Filename
// only for round-trip verification against the response.
type FileUploadResponse struct {
	Object     string `json:"object"`
	ID         string `json:"id"`
	UploadURL  string `json:"upload_url"`
	Status     string `json:"status"`
	ExpiryTime string `json:"expiry_time,omitempty"`
	Filename   string `json:"filename,omitempty"`
}

// FileClient is the typed resource client for the Notion file-uploads API.
//
// The methods currently return ErrFileUploadNotSupported because the pinned
// Notion-Version does not expose the /v1/file_uploads endpoint. The client
// shape is kept stable so issue #11 can flip the implementation without
// breaking callers.
type FileClient struct {
	c *Client
}

// NewFileClient wraps a *Client with file-upload methods.
func NewFileClient(c *Client) *FileClient {
	return &FileClient{c: c}
}

// checkAuth mirrors PageClient.checkAuth so missing-credential errors surface
// as ErrMissingAPIKey rather than a bare Notion 401.
func (f *FileClient) checkAuth() error {
	if f == nil || f.c == nil || f.c.apiKey == "" {
		return ErrMissingAPIKey
	}
	return nil
}

// validateUploadPath performs the client-side checks that apply regardless of
// whether the real upload flow is wired. Exported so the cmd/ layer (and any
// future alternative transport) can reuse exactly the same rules.
//
// The checks, in order, are:
//  1. path is non-empty
//  2. path refers to an existing file (not a directory, not a symlink-to-dir)
//  3. file size is within MaxFileUploadBytes
//
// All failure paths return errors suitable for human display — they name the
// path and cite the size cap in bytes so an operator can act on them without
// reading source.
//
// Returns only error: #11's real implementation will re-stat the file when
// it needs content_length for the multipart PUT, so there is no callsite
// that benefits from an os.FileInfo return today.
func validateUploadPath(path string) error {
	if path == "" {
		return fmt.Errorf("file upload: path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("file upload: stat %q: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("file upload: %q is a directory, not a file", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("file upload: %q is not a regular file", path)
	}
	if info.Size() > MaxFileUploadBytes {
		return fmt.Errorf("file upload: %q is %d bytes, exceeds client cap of %d bytes", path, info.Size(), MaxFileUploadBytes)
	}
	return nil
}

// Upload is the primary entry point for a file upload. It validates the path
// client-side, then attempts the two-step Notion upload. On the pinned
// Notion-Version it always returns ErrFileUploadNotSupported AFTER successful
// path validation, so callers that feed bad paths still get a specific path
// error (easier to act on than the sentinel). Issue #11 will flip the body
// to perform:
//
//  1. POST /v1/file_uploads with a FileUploadRequest derived from path +
//     filename, reading back a FileUploadResponse.
//  2. PUT to FileUploadResponse.UploadURL with the file's bytes encoded as
//     multipart/form-data.
//  3. Return a FileRef built from the response plus the caller's filename.
//
// The returned FileRef is safe to feed into block or page payloads that
// reference "file_upload" resources.
func (f *FileClient) Upload(ctx context.Context, path string) (*FileRef, error) {
	if err := f.checkAuth(); err != nil {
		return nil, fmt.Errorf("upload file: %w", err)
	}
	if err := validateUploadPath(path); err != nil {
		return nil, err
	}
	// Respect caller cancellation even on the stub path — zero behavioral
	// cost for the synchronous not-supported return, but it exercises ctx
	// today so #11's switchover doesn't need to re-wire context plumbing.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("upload %q: %w", filepath.Base(path), err)
	}
	return nil, fmt.Errorf("upload %q: %w", filepath.Base(path), ErrFileUploadNotSupported)
}
