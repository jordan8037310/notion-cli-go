// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

// MaxFileUploadBytes is the client-side cap applied before we contact
// Notion. Notion caps single-part uploads at 20MB (multi-part is
// larger); 20MB is a conservative default that keeps the single-part
// flow simple for v1.
const MaxFileUploadBytes int64 = 20 * 1024 * 1024

// FileRefTypeFileUpload is the Notion "type" discriminator for a file
// reference created through /v1/file_uploads.
const FileRefTypeFileUpload = "file_upload"

// FileRef is the shape returned by a successful upload. Type is always
// FileRefTypeFileUpload for files created via /v1/file_uploads.
type FileRef struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Name       string `json:"name,omitempty"`
	ExpiryTime string `json:"expiry_time,omitempty"`
}

// NewFileRef builds a FileRef with Type pinned to FileRefTypeFileUpload.
func NewFileRef(id, name string) *FileRef {
	return &FileRef{
		ID:   id,
		Type: FileRefTypeFileUpload,
		Name: name,
	}
}

// FileUploadRequest is the body for POST /v1/file_uploads. Mode selects
// the upload style — "single_part" is the default and the only mode
// this client currently exercises.
type FileUploadRequest struct {
	Mode        string `json:"mode,omitempty"`
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

// FileUploadResponse is the envelope Notion returns from
// POST /v1/file_uploads. UploadURL is the pre-signed URL to which raw
// bytes are PUT as multipart/form-data in step 2.
type FileUploadResponse struct {
	Object     string `json:"object"`
	ID         string `json:"id"`
	UploadURL  string `json:"upload_url"`
	Status     string `json:"status"`
	ExpiryTime string `json:"expiry_time,omitempty"`
	Filename   string `json:"filename,omitempty"`
}

// FileClient is the typed resource client for the Notion file-uploads
// API. Requires Notion-Version 2026-03-11 or newer.
type FileClient struct {
	c *Client
}

// NewFileClient wraps a *Client with file-upload methods.
func NewFileClient(c *Client) *FileClient {
	return &FileClient{c: c}
}

// checkAuth mirrors PageClient.checkAuth so missing-credential errors
// surface as ErrMissingAPIKey.
func (f *FileClient) checkAuth() error {
	if f == nil || f.c == nil || f.c.apiKey == "" {
		return ErrMissingAPIKey
	}
	return nil
}

// validateUploadPath performs the client-side checks that apply
// regardless of whether the real upload flow is wired.
//
// Checks, in order:
//  1. path is non-empty
//  2. path refers to an existing regular file
//  3. file size is within MaxFileUploadBytes
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

// Upload runs the two-step Notion file-upload flow:
//  1. POST /v1/file_uploads (mode=single_part, filename) → FileUploadResponse
//  2. PUT FileUploadResponse.UploadURL with multipart/form-data containing
//     the file bytes under the "file" field
//
// Returns a FileRef suitable for use as a block/page icon/cover reference.
// Caller cancellation is honored on both requests.
func (f *FileClient) Upload(ctx context.Context, path string) (*FileRef, error) {
	if err := f.checkAuth(); err != nil {
		return nil, fmt.Errorf("upload file: %w", err)
	}
	if err := validateUploadPath(path); err != nil {
		return nil, err
	}
	// Fail fast on an already-cancelled ctx so we don't open the file
	// or hit the network unnecessarily.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("upload %q: %w", filepath.Base(path), err)
	}

	filename := filepath.Base(path)

	// Step 1: request an upload slot.
	createReq := FileUploadRequest{
		Mode:     "single_part",
		Filename: filename,
	}
	httpReq, err := f.c.newRequest(ctx, http.MethodPost, "/file_uploads", createReq)
	if err != nil {
		return nil, fmt.Errorf("upload %q: %w", filename, err)
	}
	resp, err := f.c.do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("upload %q: create: %w", filename, err)
	}
	var createResp FileUploadResponse
	if err := decodeInto(resp, &createResp); err != nil {
		return nil, fmt.Errorf("upload %q: create: %w", filename, err)
	}
	if createResp.ID == "" || createResp.UploadURL == "" {
		return nil, fmt.Errorf("upload %q: create response missing id or upload_url", filename)
	}

	// Step 2: PUT the file bytes to the pre-signed URL as
	// multipart/form-data. Notion's docs specify the field name "file".
	if err := f.putMultipart(ctx, createResp.UploadURL, path, filename); err != nil {
		return nil, fmt.Errorf("upload %q: send: %w", filename, err)
	}

	ref := NewFileRef(createResp.ID, filename)
	ref.ExpiryTime = createResp.ExpiryTime
	return ref, nil
}

// putMultipart streams the file at path to the given uploadURL as a
// multipart/form-data PUT with a single "file" part. The body is
// buffered in memory (bounded by MaxFileUploadBytes) so the multipart
// Content-Length is known ahead of the Notion API's strict parser.
func (f *FileClient) putMultipart(ctx context.Context, uploadURL, path, filename string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer file.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return fmt.Errorf("build multipart: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("copy file bytes: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("close multipart: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	// The pre-signed URL expects the same auth + Notion-Version
	// headers as the REST endpoints; include them so the upload is
	// attributed to this integration.
	req.Header.Set("Authorization", "Bearer "+f.c.apiKey)
	req.Header.Set("Notion-Version", f.c.apiVersion)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := f.c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(b))
	}
	// Notion returns the updated file upload object here; we don't
	// need any fields from it (the ID already came from step 1), but
	// drain the body for connection reuse.
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
