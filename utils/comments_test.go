package utils

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCommentClient_MissingAPIKey verifies List/ListPage/Create surface
// ErrMissingAPIKey instead of panicking on nil client or falling through
// to a remote 401. Closes the CommentClient half of #54.
func TestCommentClient_MissingAPIKey(t *testing.T) {
	client := NewCommentClient(NewClient("", WithBaseURL("http://127.0.0.1:0")))

	if _, err := client.List(context.Background(), "block-id"); !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("List: err = %v, want errors.Is ErrMissingAPIKey", err)
	}
	if _, err := client.ListPage(context.Background(), "block-id", ""); !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("ListPage: err = %v, want errors.Is ErrMissingAPIKey", err)
	}
	req := CreateCommentRequest{Parent: &CommentParent{PageID: "p"}, RichText: NewCommentRichText("hi")}
	if _, err := client.Create(context.Background(), req); !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("Create: err = %v, want errors.Is ErrMissingAPIKey", err)
	}
}

// TestCommentClient_NilClientNoPanic confirms NewCommentClient(nil) does
// not panic on first call.
func TestCommentClient_NilClientNoPanic(t *testing.T) {
	client := NewCommentClient(nil)
	if _, err := client.List(context.Background(), "block-id"); !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("List(nil client): err = %v, want errors.Is ErrMissingAPIKey", err)
	}
	req := CreateCommentRequest{Parent: &CommentParent{PageID: "p"}, RichText: NewCommentRichText("hi")}
	if _, err := client.Create(context.Background(), req); !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("Create(nil client): err = %v, want errors.Is ErrMissingAPIKey", err)
	}
}

// commentsMock is a dedicated httptest server for the comments endpoints.
// It keeps per-test call counts so tests can assert pagination actually
// followed through rather than just returning a single page.
type commentsMock struct {
	srv               *httptest.Server
	listCalls         int
	lastBlockID       string
	lastStartCursor   string
	createCalls       int
	lastCreateBody    []byte
	paginatedCallsSeq []string // captures each start_cursor value in order
}

// newCommentsMock routes by (method, path, query). It intentionally fails
// loudly on unknown routes so a typo surfaces as a 404 rather than a
// silently wrong-shape response.
func newCommentsMock(t *testing.T) *commentsMock {
	t.Helper()
	m := &commentsMock{}

	writeJSON := func(w http.ResponseWriter, v interface{}) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/comments":
			m.listCalls++
			m.lastBlockID = r.URL.Query().Get("block_id")
			m.lastStartCursor = r.URL.Query().Get("start_cursor")
			m.paginatedCallsSeq = append(m.paginatedCallsSeq, m.lastStartCursor)

			switch m.lastBlockID {
			case "emptyBlock":
				writeJSON(w, CommentList{Object: "list", Results: []Comment{}})
			case "singlePage":
				writeJSON(w, CommentList{
					Object: "list",
					Results: []Comment{
						{Object: "comment", ID: "c-1", RichText: []RichText{{PlainText: "hello"}}},
						{Object: "comment", ID: "c-2", RichText: []RichText{{PlainText: "world"}}},
					},
				})
			case "paginated":
				if m.lastStartCursor == "" {
					writeJSON(w, CommentList{
						Object:     "list",
						Results:    []Comment{{Object: "comment", ID: "p-1"}, {Object: "comment", ID: "p-2"}},
						HasMore:    true,
						NextCursor: "cursor-xyz",
					})
					return
				}
				if m.lastStartCursor == "cursor-xyz" {
					writeJSON(w, CommentList{
						Object:  "list",
						Results: []Comment{{Object: "comment", ID: "p-3"}},
						HasMore: false,
					})
					return
				}
				http.Error(w, `{"object":"error","code":"bad_cursor"}`, http.StatusBadRequest)
			case "boom":
				http.Error(w, `{"object":"error","code":"rate_limited"}`, http.StatusTooManyRequests)
			default:
				http.Error(w, `{"object":"error","code":"not_found"}`, http.StatusNotFound)
			}

		case r.Method == http.MethodPost && r.URL.Path == "/comments":
			m.createCalls++
			body, _ := io.ReadAll(r.Body)
			m.lastCreateBody = body
			// Echo back a canned created comment.
			writeJSON(w, Comment{
				Object:      "comment",
				ID:          "c-new",
				CreatedTime: "2026-04-22T10:00:00.000Z",
				CreatedBy:   CommentUser{Object: "user", ID: "u-1"},
				RichText:    []RichText{{PlainText: "ack"}},
			})

		default:
			http.Error(w, `{"object":"error","code":"not_found"}`, http.StatusNotFound)
		}
	})

	m.srv = httptest.NewServer(handler)
	t.Cleanup(func() { m.srv.Close() })
	return m
}

// newCommentClientForTest builds a CommentClient pointed at the mock. It
// does not touch the package-level baseURL, so it is safe to run in
// parallel with other utils tests that do.
func newCommentClientForTest(srvURL string) *CommentClient {
	// Retries off: these tests assert that an error SURFACES, not that it
	// is retried, and the default policy would make each of them pay four
	// real backoffs.
	return NewCommentClient(NewClient("fake-key", WithBaseURL(srvURL), WithMaxRetries(0)))
}

// TestList is the gap-gate anchor for (*CommentClient).List; the detailed
// behavior lives in TestCommentClientList_*.
func TestList(t *testing.T) {
	m := newCommentsMock(t)
	cc := newCommentClientForTest(m.srv.URL)
	got, err := cc.List(context.Background(), "emptyBlock")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 comments, got %d", len(got))
	}
}

// TestListPage is the gap-gate anchor for (*CommentClient).ListPage.
func TestListPage(t *testing.T) {
	m := newCommentsMock(t)
	cc := newCommentClientForTest(m.srv.URL)
	page, err := cc.ListPage(context.Background(), "singlePage", "")
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if len(page.Results) != 2 {
		t.Errorf("expected 2 results, got %d", len(page.Results))
	}
}

// TestCreate is the gap-gate anchor for (*CommentClient).Create.
func TestCreate(t *testing.T) {
	m := newCommentsMock(t)
	cc := newCommentClientForTest(m.srv.URL)
	got, err := cc.Create(context.Background(), CreateCommentRequest{
		Parent:   &CommentParent{PageID: "p-1"},
		RichText: NewCommentRichText("hi"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ID != "c-new" {
		t.Errorf("got.ID = %q, want c-new", got.ID)
	}
}

func TestCommentClientList_Empty(t *testing.T) {
	m := newCommentsMock(t)
	cc := newCommentClientForTest(m.srv.URL)

	got, err := cc.List(context.Background(), "emptyBlock")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 comments, got %d", len(got))
	}
	if m.listCalls != 1 {
		t.Errorf("expected 1 API call, got %d", m.listCalls)
	}
}

func TestCommentClientList_SinglePage(t *testing.T) {
	m := newCommentsMock(t)
	cc := newCommentClientForTest(m.srv.URL)

	got, err := cc.List(context.Background(), "singlePage")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(got))
	}
	if got[0].ID != "c-1" || got[1].ID != "c-2" {
		t.Errorf("unexpected ids: %+v", got)
	}
	if m.listCalls != 1 {
		t.Errorf("expected 1 API call, got %d", m.listCalls)
	}
	if m.lastBlockID != "singlePage" {
		t.Errorf("block_id query not propagated: got %q", m.lastBlockID)
	}
}

func TestCommentClientList_Paginated(t *testing.T) {
	m := newCommentsMock(t)
	cc := newCommentClientForTest(m.srv.URL)

	got, err := cc.List(context.Background(), "paginated")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 comments across pages, got %d", len(got))
	}
	want := []string{"p-1", "p-2", "p-3"}
	for i, c := range got {
		if c.ID != want[i] {
			t.Errorf("result[%d].ID = %q, want %q", i, c.ID, want[i])
		}
	}
	if m.listCalls != 2 {
		t.Errorf("expected 2 API calls (one per page), got %d", m.listCalls)
	}
	if len(m.paginatedCallsSeq) != 2 || m.paginatedCallsSeq[0] != "" || m.paginatedCallsSeq[1] != "cursor-xyz" {
		t.Errorf("unexpected cursor sequence: %+v", m.paginatedCallsSeq)
	}
}

func TestCommentClientList_EmptyBlockIDRejected(t *testing.T) {
	m := newCommentsMock(t)
	cc := newCommentClientForTest(m.srv.URL)

	if _, err := cc.List(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty block id, got nil")
	}
	if m.listCalls != 0 {
		t.Errorf("expected no API calls on validation failure, got %d", m.listCalls)
	}
}

func TestCommentClientList_APIError(t *testing.T) {
	m := newCommentsMock(t)
	cc := newCommentClientForTest(m.srv.URL)

	_, err := cc.List(context.Background(), "boom")
	if err == nil {
		t.Fatal("expected API error, got nil")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("expected 429 in error, got: %v", err)
	}
}

func TestCommentClientListPage_SingleFetch(t *testing.T) {
	m := newCommentsMock(t)
	cc := newCommentClientForTest(m.srv.URL)

	page, err := cc.ListPage(context.Background(), "paginated", "")
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if len(page.Results) != 2 {
		t.Errorf("expected 2 results on page 1, got %d", len(page.Results))
	}
	if !page.HasMore || page.NextCursor != "cursor-xyz" {
		t.Errorf("unexpected pagination meta: %+v", page)
	}
	if m.listCalls != 1 {
		t.Errorf("ListPage should issue exactly one call, got %d", m.listCalls)
	}
}

func TestCommentClientListPage_EmptyBlockIDRejected(t *testing.T) {
	m := newCommentsMock(t)
	cc := newCommentClientForTest(m.srv.URL)

	if _, err := cc.ListPage(context.Background(), "", ""); err == nil {
		t.Fatal("expected error for empty block id, got nil")
	}
}

func TestCommentClientCreate_TopLevelPageID(t *testing.T) {
	m := newCommentsMock(t)
	cc := newCommentClientForTest(m.srv.URL)

	req := CreateCommentRequest{
		Parent:   &CommentParent{PageID: "page-123"},
		RichText: NewCommentRichText("hi there"),
	}
	got, err := cc.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ID != "c-new" {
		t.Errorf("got.ID = %q, want c-new", got.ID)
	}
	if m.createCalls != 1 {
		t.Errorf("expected 1 POST, got %d", m.createCalls)
	}
	body := string(m.lastCreateBody)
	if !strings.Contains(body, `"page_id":"page-123"`) {
		t.Errorf("body missing page_id: %s", body)
	}
	if strings.Contains(body, "discussion_id") {
		t.Errorf("body should not include discussion_id: %s", body)
	}
}

// TestCommentClientCreate_OmitsEmptyAnnotationColor pins the wire shape
// for #49: when callers build a rich_text run via NewCommentRichText (or
// any other zero-Annotation path), the JSON body must NOT include a
// `color` field. Notion-Version 2026-03-11 rejects `"color": ""` with
// `body.rich_text[0].annotations.color should be "default", ... or undefined`.
// The fix is `,omitempty` on Annotation.Color; this test guards against
// a future revert that drops the tag.
func TestCommentClientCreate_OmitsEmptyAnnotationColor(t *testing.T) {
	m := newCommentsMock(t)
	cc := newCommentClientForTest(m.srv.URL)

	req := CreateCommentRequest{
		Parent:   &CommentParent{PageID: "page-123"},
		RichText: NewCommentRichText("hello"),
	}
	if _, err := cc.Create(context.Background(), req); err != nil {
		t.Fatalf("Create: %v", err)
	}

	body := string(m.lastCreateBody)
	// The empty-string color is the exact byte sequence Notion 400s on.
	// Match the substring rather than parsing JSON so the assertion is
	// scoped to the wire form, not just the decoded shape.
	if strings.Contains(body, `"color":""`) {
		t.Errorf("comments create body must not include empty color field — Notion 2026-03-11 rejects it. body=%s", body)
	}
	// Sanity: the bool annotation flags are still expected (Notion
	// accepts them as false). If they ever get omitempty too, this
	// assertion would catch a wider behavior change.
	if !strings.Contains(body, `"bold":false`) {
		t.Errorf("comments create body should still include bold=false. body=%s", body)
	}
}

// TestCommentClientCreate_PreservesNonDefaultColor confirms that callers
// who DO set a color (e.g. mention runs that round-trip through
// CommentClient) still get the field on the wire.
func TestCommentClientCreate_PreservesNonDefaultColor(t *testing.T) {
	m := newCommentsMock(t)
	cc := newCommentClientForTest(m.srv.URL)

	rt := NewCommentRichText("hello")
	rt[0].Annotations.Color = "red"

	req := CreateCommentRequest{
		Parent:   &CommentParent{PageID: "page-123"},
		RichText: rt,
	}
	if _, err := cc.Create(context.Background(), req); err != nil {
		t.Fatalf("Create: %v", err)
	}
	body := string(m.lastCreateBody)
	if !strings.Contains(body, `"color":"red"`) {
		t.Errorf("non-empty color should still serialise. body=%s", body)
	}
}

func TestCommentClientCreate_TopLevelBlockID(t *testing.T) {
	m := newCommentsMock(t)
	cc := newCommentClientForTest(m.srv.URL)

	req := CreateCommentRequest{
		Parent:   &CommentParent{BlockID: "block-456"},
		RichText: NewCommentRichText("hi block"),
	}
	if _, err := cc.Create(context.Background(), req); err != nil {
		t.Fatalf("Create: %v", err)
	}
	body := string(m.lastCreateBody)
	if !strings.Contains(body, `"block_id":"block-456"`) {
		t.Errorf("body missing block_id: %s", body)
	}
}

func TestCommentClientCreate_Reply(t *testing.T) {
	m := newCommentsMock(t)
	cc := newCommentClientForTest(m.srv.URL)

	req := CreateCommentRequest{
		DiscussionID: "disc-789",
		RichText:     NewCommentRichText("reply"),
	}
	if _, err := cc.Create(context.Background(), req); err != nil {
		t.Fatalf("Create: %v", err)
	}
	body := string(m.lastCreateBody)
	if !strings.Contains(body, `"discussion_id":"disc-789"`) {
		t.Errorf("body missing discussion_id: %s", body)
	}
	if strings.Contains(body, `"parent"`) {
		t.Errorf("body should not include parent on a reply: %s", body)
	}
}

func TestCommentClientCreate_ValidationErrors(t *testing.T) {
	m := newCommentsMock(t)
	cc := newCommentClientForTest(m.srv.URL)

	tests := []struct {
		name    string
		req     CreateCommentRequest
		wantSub string
	}{
		{
			name:    "neither parent nor discussion",
			req:     CreateCommentRequest{RichText: NewCommentRichText("x")},
			wantSub: "parent.page_id/block_id or discussion_id",
		},
		{
			name: "both parent and discussion",
			req: CreateCommentRequest{
				Parent:       &CommentParent{PageID: "p"},
				DiscussionID: "d",
				RichText:     NewCommentRichText("x"),
			},
			wantSub: "mutually exclusive",
		},
		{
			name: "parent with both page and block",
			req: CreateCommentRequest{
				Parent:   &CommentParent{PageID: "p", BlockID: "b"},
				RichText: NewCommentRichText("x"),
			},
			wantSub: "mutually exclusive",
		},
		{
			name:    "missing rich_text",
			req:     CreateCommentRequest{Parent: &CommentParent{PageID: "p"}},
			wantSub: "rich_text is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := cc.Create(context.Background(), tt.req); err == nil {
				t.Fatal("expected validation error, got nil")
			} else if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("err = %q, want substring %q", err.Error(), tt.wantSub)
			}
		})
	}
	if m.createCalls != 0 {
		t.Errorf("validation errors must not issue a POST; got %d", m.createCalls)
	}
}

func TestNewCommentRichText(t *testing.T) {
	rt := NewCommentRichText("hello")
	if len(rt) != 1 {
		t.Fatalf("expected 1 run, got %d", len(rt))
	}
	if rt[0].Type != "text" || rt[0].Text.Content != "hello" || rt[0].PlainText != "hello" {
		t.Errorf("unexpected run: %+v", rt[0])
	}
}
