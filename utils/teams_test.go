package utils

import (
	"context"
	"errors"
	"testing"
)

// TestNewTeamClient is a smoke test for the constructor; it exists so the
// gap-check script sees a matching Test function for the exported
// NewTeamClient symbol.
func TestNewTeamClient(t *testing.T) {
	c := NewClient("k", WithBaseURL("http://example"))
	got := NewTeamClient(c)
	if got == nil || got.c != c {
		t.Fatalf("NewTeamClient: %+v", got)
	}
}

// TestTeamClient_List_ReturnsNotSupported asserts the stub surfaces a
// typed error referencing the pinned API version / issue #11. Verified
// via errors.Is so callers can branch without string-matching.
func TestTeamClient_List_ReturnsNotSupported(t *testing.T) {
	client := NewTeamClient(NewClient("k", WithBaseURL("http://127.0.0.1:0")))
	got, err := client.List(context.Background())
	if err == nil {
		t.Fatalf("List: want error, got teams %+v", got)
	}
	if !errors.Is(err, ErrTeamsNotSupported) {
		t.Errorf("List: want errors.Is ErrTeamsNotSupported, got %v", err)
	}
	if got != nil {
		t.Errorf("List: want nil teams, got %+v", got)
	}
}

// TestTeamClient_ListPage_ReturnsNotSupported covers the per-page
// accessor for parity with List.
func TestTeamClient_ListPage_ReturnsNotSupported(t *testing.T) {
	client := NewTeamClient(NewClient("k", WithBaseURL("http://127.0.0.1:0")))
	got, err := client.ListPage(context.Background(), "")
	if err == nil {
		t.Fatalf("ListPage: want error, got %+v", got)
	}
	if !errors.Is(err, ErrTeamsNotSupported) {
		t.Errorf("ListPage: want errors.Is ErrTeamsNotSupported, got %v", err)
	}
	if got != nil {
		t.Errorf("ListPage: want nil page, got %+v", got)
	}
}

// TestErrTeamsNotSupported_MessageReferences11 asserts the error
// message mentions the pinned version and issue #11 so users can find
// the tracking issue without reading the code.
func TestErrTeamsNotSupported_MessageReferences11(t *testing.T) {
	msg := ErrTeamsNotSupported.Error()
	if !containsAll(msg, NotionAPIVersion, "#11") {
		t.Errorf("ErrTeamsNotSupported message = %q; want it to mention %q and %q", msg, NotionAPIVersion, "#11")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(sub) == 0 || indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	if m > n {
		return -1
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}
