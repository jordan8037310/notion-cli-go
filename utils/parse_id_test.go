// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"strings"
	"testing"
)

// TestParseNotionID_Forms covers every input shape the fetch dispatcher
// promises to accept plus the negative paths. The expected output is
// always the canonical dashed form so callers can build paths like
// /v1/pages/<id> without re-normalising.
func TestParseNotionID_Forms(t *testing.T) {
	const (
		hex    = "abc123def4567890abc123def4567890"
		dashed = "abc123de-f456-7890-abc1-23def4567890"
	)

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"bare hex", hex, dashed},
		{"already dashed", dashed, dashed},
		{"https www", "https://www.notion.so/Workspace/Page-Title-" + hex, dashed},
		{"https no www", "https://notion.so/" + hex, dashed},
		{"protocol-less", "notion.so/" + hex, dashed},
		{"with query", "https://www.notion.so/" + hex + "?v=" + hex, dashed},
		{"with fragment", "https://www.notion.so/Workspace/Page-Title-" + hex + "#section", dashed},
		{"surrounding whitespace", "  " + hex + "\n", dashed},
		{"uppercase preserved", strings.ToUpper(hex), strings.ToUpper(hex)[0:8] + "-" + strings.ToUpper(hex)[8:12] + "-" + strings.ToUpper(hex)[12:16] + "-" + strings.ToUpper(hex)[16:20] + "-" + strings.ToUpper(hex)[20:32]},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseNotionID(tc.input)
			if err != nil {
				t.Fatalf("ParseNotionID(%q) returned error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("ParseNotionID(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestParseNotionID_Errors confirms malformed inputs surface a descriptive
// error instead of returning a partial id or panicking.
func TestParseNotionID_Errors(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"too short", "abc123"},
		{"non-hex content", "not-a-real-id"},
		{"url with no id", "https://www.notion.so/Some-Page-Title"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseNotionID(tc.input); err == nil {
				t.Errorf("ParseNotionID(%q) expected error, got nil", tc.input)
			}
		})
	}
}
