package utils

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Mock server
var (
	mockServer    *httptest.Server
	deletedBlocks map[string]bool
)

func SetBaseURL(url string) {
	baseURL = url
}

func mockBlock(texts []string) Block {

	richTexts := make([]RichText, len(texts))
	for i, text := range texts {
		richTexts[i] = RichText{PlainText: text}
	}

	return Block{
		Object: "block",
		ID:     "blockID",
		Type:   "to_do",
		ToDo: &ToDo{
			Checked:  false,
			RichText: richTexts,
		},
	}
}

func setup() {
	deletedBlocks = make(map[string]bool)
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("{}")) // Send back an empty JSON body for DELETE requests
		case r.URL.Path == "/blocks/pageID/children":

			result := BlockList{
				Results: []Block{
					mockBlock([]string{"test todo"}),
				},
			}
			err := json.NewEncoder(w).Encode(result)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}

		case r.URL.Path == "/blocks/pageWithToDoWithNoContent/children":

			result := BlockList{
				Results: []Block{
					mockBlock([]string{}),
					mockBlock([]string{"test todo"}),
				},
			}
			err := json.NewEncoder(w).Encode(result)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		case r.URL.Path == "/blocks/blockID":

			result := Block{
				Object: "block",
				ID:     "blockID",
				Type:   "to_do",
				ToDo: &ToDo{
					Checked:  true,
					RichText: []RichText{{PlainText: "test todo"}},
				},
			}
			err := json.NewEncoder(w).Encode(result)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		}
	}))
	SetBaseURL(mockServer.URL)
}

func teardown() {
	mockServer.Close()
}

func TestGetBlocks(t *testing.T) {
	setup()
	defer teardown()

	notionAPIKey := "fakeKey"
	pageID := "pageID"
	blocks, err := GetBlocks(notionAPIKey, pageID)

	if err != nil {
		t.Errorf("Got error: %v", err)
	}

	if len(blocks) != 1 {
		t.Errorf("Expected 1 block, got: %v", len(blocks))
	}
}

func TestGetBlocksIfRichTextIsEmpty(t *testing.T) {
	setup()
	defer teardown()
	notionAPIKey := "fakeKey"
	pageID := "pageWithToDoWithNoContent"
	blocks, err := GetBlocks(notionAPIKey, pageID)

	if err != nil {
		t.Errorf("Got error: %v", err)
	}

	if len(blocks) != 1 {
		t.Errorf("Expected 1 block, got: %v", len(blocks))
	}

}

func TestAddNewToDoItem(t *testing.T) {
	setup()
	defer teardown()

	notionAPIKey := "fakeKey"
	pageID := "pageID"
	toDoText := "new todo"
	err := AddNewToDoItem(notionAPIKey, pageID, toDoText)

	if err != nil {
		t.Errorf("Got error: %v", err)
	}
}

func TestDeleteToDoBlock(t *testing.T) {
	setup()
	defer teardown()

	notionAPIKey := "fakeKey"
	pageID := "pageID"
	order := 1

	err := DeleteToDoBlock(notionAPIKey, pageID, order)

	if err != nil {
		t.Errorf("Error deleting to-do block with ID %s and order %d: %v", pageID, order, err)
	}

}

// Tests for block types functionality

func TestIsValidBlockType(t *testing.T) {
	validTypes := []string{
		"paragraph", "heading_1", "heading_2", "heading_3",
		"bulleted_list_item", "numbered_list_item", "to_do",
		"toggle", "quote", "callout", "divider", "code",
	}

	for _, bt := range validTypes {
		if !IsValidBlockType(bt) {
			t.Errorf("Expected '%s' to be a valid block type", bt)
		}
	}

	invalidTypes := []string{"invalid", "unknown", "image", ""}
	for _, bt := range invalidTypes {
		if IsValidBlockType(bt) {
			t.Errorf("Expected '%s' to be an invalid block type", bt)
		}
	}
}

func TestGetSupportedBlockTypeNames(t *testing.T) {
	names := GetSupportedBlockTypeNames()

	if len(names) != 12 {
		t.Errorf("Expected 12 block types, got %d", len(names))
	}

	// Check that list is sorted
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("Block type names not sorted: %s < %s", names[i], names[i-1])
		}
	}
}

func TestGetBlockContent(t *testing.T) {
	tests := []struct {
		name     string
		block    Block
		expected string
	}{
		{
			name: "to_do block",
			block: Block{
				Type: "to_do",
				ToDo: &ToDo{RichText: []RichText{{PlainText: "Buy milk"}}},
			},
			expected: "Buy milk",
		},
		{
			name: "paragraph block",
			block: Block{
				Type:      "paragraph",
				Paragraph: &RichTextBlock{RichText: []RichText{{PlainText: "Hello world"}}},
			},
			expected: "Hello world",
		},
		{
			name: "heading_1 block",
			block: Block{
				Type:     "heading_1",
				Heading1: &RichTextBlock{RichText: []RichText{{PlainText: "Title"}}},
			},
			expected: "Title",
		},
		{
			name: "divider block",
			block: Block{
				Type:    "divider",
				Divider: &struct{}{},
			},
			expected: "───────────",
		},
		{
			name: "empty paragraph",
			block: Block{
				Type:      "paragraph",
				Paragraph: &RichTextBlock{RichText: []RichText{}},
			},
			expected: "(empty)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetBlockContent(tt.block)
			if result != tt.expected {
				t.Errorf("GetBlockContent() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestGetBlockIcon(t *testing.T) {
	tests := []struct {
		name     string
		block    Block
		expected string
	}{
		{
			name:     "unchecked to_do",
			block:    Block{Type: "to_do", ToDo: &ToDo{Checked: false}},
			expected: "☐",
		},
		{
			name:     "checked to_do",
			block:    Block{Type: "to_do", ToDo: &ToDo{Checked: true}},
			expected: "☑",
		},
		{
			name:     "paragraph",
			block:    Block{Type: "paragraph"},
			expected: "¶",
		},
		{
			name:     "heading_1",
			block:    Block{Type: "heading_1"},
			expected: "H1",
		},
		{
			name:     "bulleted_list_item",
			block:    Block{Type: "bulleted_list_item"},
			expected: "•",
		},
		{
			name:     "unknown type",
			block:    Block{Type: "unknown"},
			expected: "?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetBlockIcon(tt.block)
			if result != tt.expected {
				t.Errorf("GetBlockIcon() = %q, want %q", result, tt.expected)
			}
		})
	}
}
