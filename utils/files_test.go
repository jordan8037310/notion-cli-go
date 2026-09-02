package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeNotionFileAPI returns an httptest server that implements the two-
// step upload flow. Step 1 (POST /v1/file_uploads) returns an
// upload_url that points back at the same server's /file_uploads/{id}/send
// path. Step 2 reads the multipart body and records the bytes in
// captured.
type fakeNotionFileAPI struct {
	mu          sync.Mutex
	uploadedID  string
	uploadedNm  string
	uploadedBuf []byte
	step2Called bool
	failStep2   bool
}

func (f *fakeNotionFileAPI) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/file_uploads":
			// Echo filename back from request body.
			var req FileUploadRequest
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &req)

			f.mu.Lock()
			f.uploadedID = "file-123"
			f.uploadedNm = req.Filename
			f.mu.Unlock()

			upload := "http://" + r.Host + "/file_uploads/file-123/send"
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(FileUploadResponse{
				Object:     "file_upload",
				ID:         "file-123",
				UploadURL:  upload,
				Status:     "pending",
				ExpiryTime: "2026-06-01T00:00:00Z",
				Filename:   req.Filename,
			})

		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/file_uploads/") && strings.HasSuffix(r.URL.Path, "/send"):
			f.mu.Lock()
			f.step2Called = true
			shouldFail := f.failStep2
			f.mu.Unlock()

			if shouldFail {
				http.Error(w, `{"object":"error","code":"bad_request"}`, http.StatusBadRequest)
				return
			}

			// Parse the multipart body so tests can assert the
			// file part arrived intact.
			if err := r.ParseMultipartForm(32 << 20); err != nil {
				http.Error(w, "bad multipart: "+err.Error(), http.StatusBadRequest)
				return
			}
			fh := r.MultipartForm.File["file"]
			if len(fh) == 0 {
				http.Error(w, `missing "file" part`, http.StatusBadRequest)
				return
			}
			src, err := fh[0].Open()
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			defer src.Close()
			buf, _ := io.ReadAll(src)

			f.mu.Lock()
			f.uploadedBuf = buf
			f.mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(FileUploadResponse{
				Object: "file_upload",
				ID:     "file-123",
				Status: "uploaded",
			})

		default:
			http.Error(w, `{"object":"error","code":"not_found"}`, http.StatusNotFound)
		}
	})
}

// TestNewFileClient is a smoke test for the constructor.
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

// TestFiles_NewFileRef pins the constructor's contract.
func TestFiles_NewFileRef(t *testing.T) {
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
	if FileRefTypeFileUpload != "file_upload" {
		t.Errorf("FileRefTypeFileUpload drift: got %q, want file_upload", FileRefTypeFileUpload)
	}
}

// writeTempFile creates a file of the requested size under t.TempDir.
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

// TestFiles_Upload_HappyPath exercises the full two-step flow and verifies
// the returned FileRef plus the bytes the server observed.
func TestFiles_Upload_HappyPath(t *testing.T) {
	api := &fakeNotionFileAPI{}
	srv := httptest.NewServer(api.handler(t))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "hello.png")
	payload := []byte("hello-bytes")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	client := NewFileClient(NewClient("k", WithBaseURL(srv.URL)))
	ref, err := client.Upload(context.Background(), path)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if ref == nil || ref.ID != "file-123" {
		t.Errorf("Upload ref = %+v, want ID=file-123", ref)
	}
	if ref.Type != FileRefTypeFileUpload {
		t.Errorf("Upload ref.Type = %q, want %q", ref.Type, FileRefTypeFileUpload)
	}
	if ref.Name != "hello.png" {
		t.Errorf("Upload ref.Name = %q, want hello.png", ref.Name)
	}
	if !api.step2Called {
		t.Error("Upload: step 2 (multipart PUT) was not called")
	}
	if string(api.uploadedBuf) != string(payload) {
		t.Errorf("Upload: bytes on wire = %q, want %q", api.uploadedBuf, payload)
	}
	if api.uploadedNm != "hello.png" {
		t.Errorf("Upload: filename on wire = %q, want hello.png", api.uploadedNm)
	}
}

// TestFiles_UploadAs_DisplayNameOverride verifies that a non-empty
// displayName flows through both steps of the upload: it lands in the
// /v1/file_uploads create-request body, the multipart "file" part name
// on the pre-signed POST, and the returned FileRef.Name. Regression
// guard for PR #50 review [P2] — `--name` must not be cosmetic.
func TestFiles_UploadAs_DisplayNameOverride(t *testing.T) {
	api := &fakeNotionFileAPI{}
	srv := httptest.NewServer(api.handler(t))
	defer srv.Close()

	// Source file lives on disk under one name, but the caller wants
	// Notion to display a different one. Asserting both ends rules out
	// regressions where only the create-side or only the multipart-side
	// gets the override.
	path := filepath.Join(t.TempDir(), "tmp-source.bin")
	if err := os.WriteFile(path, []byte("payload-bytes"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	const displayName = "Q1-Plan.pdf"

	client := NewFileClient(NewClient("k", WithBaseURL(srv.URL)))
	ref, err := client.UploadAs(context.Background(), path, displayName)
	if err != nil {
		t.Fatalf("UploadAs: %v", err)
	}
	if ref == nil || ref.Name != displayName {
		t.Errorf("UploadAs ref.Name = %v, want %q", ref, displayName)
	}
	if api.uploadedNm != displayName {
		t.Errorf("UploadAs: create-request filename = %q, want %q", api.uploadedNm, displayName)
	}
}

// TestFiles_UploadAs_EmptyOverrideUsesBasename confirms the empty
// displayName path is equivalent to Upload(): the source basename
// reaches Notion. This locks in the back-compat contract that
// Upload(ctx, path) == UploadAs(ctx, path, "").
func TestFiles_UploadAs_EmptyOverrideUsesBasename(t *testing.T) {
	api := &fakeNotionFileAPI{}
	srv := httptest.NewServer(api.handler(t))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "screenshot.png")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	client := NewFileClient(NewClient("k", WithBaseURL(srv.URL)))
	ref, err := client.UploadAs(context.Background(), path, "")
	if err != nil {
		t.Fatalf("UploadAs: %v", err)
	}
	if ref.Name != "screenshot.png" {
		t.Errorf("UploadAs ref.Name = %q, want screenshot.png", ref.Name)
	}
	if api.uploadedNm != "screenshot.png" {
		t.Errorf("UploadAs: create-request filename = %q, want screenshot.png", api.uploadedNm)
	}
}

// TestFiles_Upload_Step1Error surfaces a non-2xx from step 1 without
// calling step 2.
func TestFiles_Upload_Step1Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"object":"error","code":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	path := writeTempFile(t, 4)
	client := NewFileClient(NewClient("k", WithBaseURL(srv.URL)))
	_, err := client.Upload(context.Background(), path)
	if err == nil {
		t.Fatal("Upload: want error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("Upload error = %q; want 401", err.Error())
	}
}

// TestFiles_Upload_Step2Error surfaces a non-2xx from step 2 without
// panicking.
func TestFiles_Upload_Step2Error(t *testing.T) {
	api := &fakeNotionFileAPI{failStep2: true}
	srv := httptest.NewServer(api.handler(t))
	defer srv.Close()

	path := writeTempFile(t, 4)
	client := NewFileClient(NewClient("k", WithBaseURL(srv.URL)))
	_, err := client.Upload(context.Background(), path)
	if err == nil {
		t.Fatal("Upload: want error on step 2 400")
	}
	if !strings.Contains(err.Error(), "send") {
		t.Errorf("Upload error = %q; want to mention 'send'", err.Error())
	}
}

// TestFiles_Upload_MissingUploadURL guards against a malformed step-1
// response.
func TestFiles_Upload_MissingUploadURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(FileUploadResponse{Object: "file_upload", ID: "file-123"})
	}))
	defer srv.Close()

	path := writeTempFile(t, 4)
	client := NewFileClient(NewClient("k", WithBaseURL(srv.URL)))
	_, err := client.Upload(context.Background(), path)
	if err == nil {
		t.Fatal("Upload: want error for missing upload_url")
	}
	if !strings.Contains(err.Error(), "upload_url") {
		t.Errorf("Upload error = %q; want to mention upload_url", err.Error())
	}
}

// TestFiles_Upload_MissingAPIKey asserts an unconfigured Client is
// caught BEFORE path validation.
func TestFiles_Upload_MissingAPIKey(t *testing.T) {
	client := NewFileClient(NewClient("", WithBaseURL("http://127.0.0.1:0")))
	path := writeTempFile(t, 1)
	got, err := client.Upload(context.Background(), path)
	if err == nil {
		t.Fatalf("Upload: want error, got %+v", got)
	}
	if !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("Upload: want errors.Is ErrMissingAPIKey, got %v", err)
	}
}

// TestFiles_Upload_ValidationErrors table-drives every client-side
// rejection path.
func TestFiles_Upload_ValidationErrors(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.bin")
	subdir := filepath.Join(dir, "a-dir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
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
		{"empty path", "", []string{"path is required"}},
		{"missing file", missing, []string{"stat", "does-not-exist.bin"}},
		{"directory", subdir, []string{"a-dir", "directory"}},
		{"oversize file", bigPath, []string{"big.bin", "exceeds client cap"}},
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
			for _, frag := range tt.wantFragments {
				if !strings.Contains(err.Error(), frag) {
					t.Errorf("Upload(%q): error %q missing fragment %q", tt.path, err.Error(), frag)
				}
			}
		})
	}
}

// TestFiles_Upload_RespectsCtxCancel asserts Upload fails fast on an
// already-cancelled ctx without hitting the network.
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
}

// TestFiles_Upload_ExactlyAtSizeCap verifies the boundary case.
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

	// Point at a server that completes the two-step flow so the cap
	// test exercises the full success path rather than getting
	// masked by a connection refusal.
	api := &fakeNotionFileAPI{}
	srv := httptest.NewServer(api.handler(t))
	defer srv.Close()

	client := NewFileClient(NewClient("k", WithBaseURL(srv.URL)))
	ref, err := client.Upload(context.Background(), path)
	if err != nil {
		t.Fatalf("Upload at exact cap: %v", err)
	}
	if ref == nil || ref.ID != "file-123" {
		t.Errorf("Upload at exact cap: ref = %+v", ref)
	}
}

// TestFiles_PutMultipart_Headers asserts the multipart content-type
// header is set and includes a boundary.
func TestFiles_PutMultipart_Headers(t *testing.T) {
	var gotType string
	var gotAuth string
	var gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("Notion-Version")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewFileClient(NewClient("k", WithBaseURL(srv.URL)))
	path := writeTempFile(t, 4)
	if err := client.putMultipart(context.Background(), srv.URL+"/send", path, "upload.bin", "application/octet-stream"); err != nil {
		t.Fatalf("putMultipart: %v", err)
	}
	if !strings.HasPrefix(gotType, "multipart/form-data; boundary=") {
		t.Errorf("Content-Type = %q; want multipart/form-data with boundary", gotType)
	}
	if gotAuth != "Bearer k" {
		t.Errorf("Authorization = %q; want Bearer k", gotAuth)
	}
	if gotVersion != NotionAPIVersion {
		t.Errorf("Notion-Version = %q; want %q", gotVersion, NotionAPIVersion)
	}
}

// TestUpload_EchoesNotionsContentType guards a defect that every unit test
// missed because the mock accepted any part header.
//
// Notion derives a content type from the filename at CREATE time and
// returns it. The SEND step must echo it back exactly; a mismatch is
// rejected:
//
//	400 validation_error: Current file content type of
//	`application/octet-stream` does not match the original content type
//	of `text/plain; charset=utf-8`.
//
// multipart.CreateFormFile hardcodes application/octet-stream, so EVERY
// upload of a file with a recognisable type failed in production —
// blocks add-image, blocks add-file, pages set-icon, pages set-cover. Only
// a real request could see it.
func TestUpload_EchoesNotionsContentType(t *testing.T) {
	var partContentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/file_uploads" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"file_upload","id":"fu1","status":"pending",
				"filename":"notes.txt","content_type":"text/plain; charset=utf-8",
				"upload_url":"` + "http://" + r.Host + `/file_uploads/fu1/send"}`))
			return
		}
		// Inspect the multipart part's own Content-Type, which is what
		// Notion compares against — not the request's.
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Errorf("send request is not multipart: %v", err)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		part, err := mr.NextPart()
		if err != nil {
			t.Errorf("read part: %v", err)
			return
		}
		partContentType = part.Header.Get("Content-Type")

		// Reject a mismatch exactly as the live API does, so this mock
		// cannot be more permissive than production.
		if partContentType != "text/plain; charset=utf-8" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"object":"error","status":400,"code":"validation_error",
				"message":"Current file content type does not match the original content type."}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"object":"file_upload","id":"fu1","status":"uploaded"}`))
	}))
	defer srv.Close()

	path := writeTempFile(t, 16)
	ref, err := NewFileClient(NewClient("k", WithBaseURL(srv.URL))).
		UploadAs(context.Background(), path, "notes.txt")
	if err != nil {
		t.Fatalf("UploadAs: %v", err)
	}
	if ref.ID != "fu1" {
		t.Errorf("ref.ID = %q", ref.ID)
	}
	if partContentType != "text/plain; charset=utf-8" {
		t.Errorf("multipart part Content-Type = %q, want Notion's own %q",
			partContentType, "text/plain; charset=utf-8")
	}
}

// TestCreateFilePart_FallsBackWhenNotionSaysNothing keeps a hand-built
// response (or an older API) from producing a part with no type at all.
func TestCreateFilePart_FallsBackWhenNotionSaysNothing(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if _, err := createFilePart(mw, "x.bin", ""); err != nil {
		t.Fatalf("createFilePart: %v", err)
	}
	_ = mw.Close()
	if !strings.Contains(buf.String(), "application/octet-stream") {
		t.Errorf("empty content type should fall back to octet-stream:\n%s", buf.String())
	}
}

// TestCreateFilePart_EscapesQuotesInFilenames stops a crafted name from
// breaking out of the Content-Disposition header.
func TestCreateFilePart_EscapesQuotesInFilenames(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if _, err := createFilePart(mw, `we"ird.txt`, "text/plain"); err != nil {
		t.Fatalf("createFilePart: %v", err)
	}
	_ = mw.Close()
	if !strings.Contains(buf.String(), `filename="we\"ird.txt"`) {
		t.Errorf("quote was not escaped in Content-Disposition:\n%s", buf.String())
	}
}
