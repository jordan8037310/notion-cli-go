// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import "testing"

// TestFindPageTitleText covers the helper that backs extractTitle and
// pagePlainTitle (cmd/fetch). Three contracts pinned here:
//
//  1. Title is found via the property's *type*, not its key, so renamed
//     title columns ("Name", "Client Name", etc.) work.
//  2. Multi-run titles concatenate every run's plain_text — closes #65
//     where the previous code returned only the first non-empty run.
//  3. Pages with no title property return ("", false) so callers can
//     fall back to a placeholder.
func TestFindPageTitleText(t *testing.T) {
	cases := []struct {
		name   string
		props  map[string]interface{}
		want   string
		wantOK bool
	}{
		{
			name: "renamed title column",
			props: map[string]interface{}{
				"Client Name": map[string]interface{}{
					"type": "title",
					"title": []interface{}{
						map[string]interface{}{"plain_text": "Acme Corp"},
					},
				},
			},
			want:   "Acme Corp",
			wantOK: true,
		},
		{
			name: "multi-run title concatenated",
			props: map[string]interface{}{
				"Name": map[string]interface{}{
					"type": "title",
					"title": []interface{}{
						map[string]interface{}{"plain_text": "Project: "},
						map[string]interface{}{"plain_text": "Q2 Plan"},
					},
				},
			},
			want:   "Project: Q2 Plan",
			wantOK: true,
		},
		{
			name: "no title property",
			props: map[string]interface{}{
				"Status": map[string]interface{}{"type": "select", "select": map[string]interface{}{"name": "Done"}},
			},
			want:   "",
			wantOK: false,
		},
		{
			name: "empty title runs",
			props: map[string]interface{}{
				"Name": map[string]interface{}{
					"type":  "title",
					"title": []interface{}{},
				},
			},
			want:   "",
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := findPageTitleText(tc.props)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("findPageTitleText = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestFindSchemaTitlePropertyKey covers the schema-side helper used by
// Create's database-parent probe. Returns the *key* of the title-typed
// property (not its value), since the key is what the request payload
// needs to use to address the column.
func TestFindSchemaTitlePropertyKey(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]interface{}
		want  string
	}{
		{
			name: "renamed to Client Name",
			props: map[string]interface{}{
				"Client Name": map[string]interface{}{"type": "title"},
				"Status":      map[string]interface{}{"type": "select"},
			},
			want: "Client Name",
		},
		{
			name: "default title key",
			props: map[string]interface{}{
				"title":  map[string]interface{}{"type": "title"},
				"Status": map[string]interface{}{"type": "select"},
			},
			want: "title",
		},
		{
			name: "no title property",
			props: map[string]interface{}{
				"Status": map[string]interface{}{"type": "select"},
			},
			want: "",
		},
		{
			name:  "empty schema",
			props: map[string]interface{}{},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := findSchemaTitlePropertyKey(tc.props); got != tc.want {
				t.Errorf("findSchemaTitlePropertyKey = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPropertiesContainTitle pins the skip-the-probe contract: when a
// caller pre-resolves the title via PR #53's typed --property surface,
// Create/Update see a title-typed entry in req.Properties and skip
// the auto-probe entirely.
func TestPropertiesContainTitle(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]interface{}
		want  bool
	}{
		{
			name: "preformed []map[string]interface{} title",
			props: map[string]interface{}{
				"Name": map[string]interface{}{
					"title": []map[string]interface{}{{"plain_text": "x"}},
				},
			},
			want: true,
		},
		{
			name: "preformed []interface{} title (round-tripped JSON)",
			props: map[string]interface{}{
				"Name": map[string]interface{}{
					"title": []interface{}{map[string]interface{}{"plain_text": "x"}},
				},
			},
			want: true,
		},
		{
			name: "no title-typed property",
			props: map[string]interface{}{
				"Status": map[string]interface{}{"select": map[string]interface{}{"name": "Done"}},
			},
			want: false,
		},
		{
			name:  "empty",
			props: map[string]interface{}{},
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := propertiesContainTitle(tc.props); got != tc.want {
				t.Errorf("propertiesContainTitle = %v, want %v", got, tc.want)
			}
		})
	}
}
