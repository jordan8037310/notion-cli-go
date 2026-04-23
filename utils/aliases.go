// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// AliasStore persists `name -> page-id` mappings to a small YAML-shaped file
// on disk so users can refer to Notion pages by short aliases instead of
// 32-hex UUIDs. The on-disk format is one `key: value` pair per line, e.g.:
//
//	work-notes: 11111111111111111111111111111111
//	journal: 22222222-2222-2222-2222-222222222222
//
// Blank lines and lines beginning with `#` are treated as comments. This is
// deliberately a minimal subset of YAML so the repo can avoid adding a
// third-party YAML dependency — a full YAML parser (nested keys, block
// scalars, flow sequences, etc.) is NOT supported. If the format ever needs
// to grow beyond flat key-value pairs, swap the file format to JSON before
// reaching for a YAML library.
type AliasStore struct {
	// Path is the absolute path to the alias file on disk. Empty Path is
	// rejected by the mutating methods; use DefaultAliasStore() to get the
	// conventional ~/.config/notioncli/pages.yaml location.
	Path string
}

// uuidPattern matches a Notion page identifier: 32 hexadecimal characters,
// optionally separated by the canonical 8-4-4-4-12 dashes. Notion accepts
// both the dashed and undashed forms in its URLs and API responses, so we
// treat both as "already an id" in Resolve.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12}$`)

// DefaultAliasStore returns an AliasStore pointed at the conventional
// `$HOME/.config/notioncli/pages.yaml` location. The returned store does
// NOT create the parent directory or the file — Load handles missing files
// as "no aliases yet", and Set creates them lazily on write.
func DefaultAliasStore() (*AliasStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("default alias store: resolve home dir: %w", err)
	}
	return &AliasStore{
		Path: filepath.Join(home, ".config", "notioncli", "pages.yaml"),
	}, nil
}

// Load reads the alias file and returns the parsed map. A missing file is
// not an error — an empty map is returned so callers can treat "no aliases
// configured yet" the same as "aliases configured but none match". A
// malformed line (no colon separator) returns a descriptive error citing
// the 1-indexed line number.
func (s *AliasStore) Load() (map[string]string, error) {
	out := make(map[string]string)
	if s == nil || s.Path == "" {
		return out, nil
	}
	f, err := os.Open(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("alias store: open %s: %w", s.Path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		raw := scanner.Text()
		line := strings.TrimSpace(raw)
		// Skip blank lines and comments.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 1 {
			return nil, fmt.Errorf("alias store: %s:%d: expected `key: value`, got %q", s.Path, lineNum, raw)
		}
		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		if key == "" {
			return nil, fmt.Errorf("alias store: %s:%d: empty key", s.Path, lineNum)
		}
		// Strip optional surrounding quotes — Notion ids are unquoted in
		// practice but we do not want a user-written `'id'` to silently
		// become part of the stored value.
		value = strings.Trim(value, `"'`)
		out[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("alias store: read %s: %w", s.Path, err)
	}
	return out, nil
}

// All is a convenience for Load. Kept distinct so the list command can
// document "All" in its call site without implying the caller intends to
// mutate the returned map.
func (s *AliasStore) All() (map[string]string, error) {
	return s.Load()
}

// Resolve returns a Notion page id given either a raw id or an alias. The
// lookup order is:
//
//  1. If nameOrID already matches the Notion UUID shape, return it verbatim
//     (no filesystem access, no error).
//  2. Otherwise, treat it as an alias name and look it up in the store.
//     A missing alias returns an error citing the name so the user can fix
//     the invocation.
//
// Empty input is rejected up front so callers do not have to.
func (s *AliasStore) Resolve(nameOrID string) (string, error) {
	if nameOrID == "" {
		return "", fmt.Errorf("alias store: empty name or id")
	}
	if uuidPattern.MatchString(nameOrID) {
		return nameOrID, nil
	}
	aliases, err := s.Load()
	if err != nil {
		return "", err
	}
	id, ok := aliases[nameOrID]
	if !ok {
		return "", fmt.Errorf("alias %q not found (run `notioncli pages add-alias %s <id>` to add it)", nameOrID, nameOrID)
	}
	return id, nil
}

// Set inserts or overwrites the given alias and persists the store to disk.
// Writes are atomic: the store is first rendered to a sibling tempfile,
// fsynced, then renamed into place so a crash mid-write cannot truncate the
// existing alias file. The parent directory is created on demand with
// 0o700 permissions because this directory lives under `~/.config/notioncli`
// alongside the user's Notion API token.
func (s *AliasStore) Set(name, id string) error {
	if s == nil || s.Path == "" {
		return fmt.Errorf("alias store: empty path")
	}
	if name == "" {
		return fmt.Errorf("alias store: empty name")
	}
	if id == "" {
		return fmt.Errorf("alias store: empty id")
	}
	// Load the existing map so Set acts as upsert.
	aliases, err := s.Load()
	if err != nil {
		return err
	}
	aliases[name] = id

	// Ensure the parent directory exists. 0o700 mirrors the permissions
	// typically used for ~/.config/<app> directories holding credentials.
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("alias store: create parent dir: %w", err)
	}

	// Render to a tempfile in the same directory, then rename for atomicity.
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".pages.*.yaml")
	if err != nil {
		return fmt.Errorf("alias store: create tempfile: %w", err)
	}
	tmpName := tmp.Name()
	// Guard against a partial rename by cleaning up the temp on any error.
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpName)
		}
	}()

	// Stable output ordering so diffs in version control (if anyone commits
	// their aliases file) stay legible.
	names := make([]string, 0, len(aliases))
	for n := range aliases {
		names = append(names, n)
	}
	sort.Strings(names)

	w := bufio.NewWriter(tmp)
	// Leading comment helps a user who opens the file by hand understand
	// what it is without having to chase the code.
	if _, err := w.WriteString("# notioncli page aliases — edit with `notioncli pages add-alias`\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("alias store: write header: %w", err)
	}
	for _, n := range names {
		if _, err := fmt.Fprintf(w, "%s: %s\n", n, aliases[n]); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("alias store: write entry: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("alias store: flush tempfile: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("alias store: fsync tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("alias store: close tempfile: %w", err)
	}
	if err := os.Rename(tmpName, s.Path); err != nil {
		return fmt.Errorf("alias store: rename tempfile: %w", err)
	}
	success = true
	return nil
}

// IsNotionID reports whether the given string matches the 32-hex-with-
// optional-dashes Notion identifier shape. Exported so cmd/ can validate
// user input without reflection into the store.
func IsNotionID(s string) bool {
	return uuidPattern.MatchString(s)
}
