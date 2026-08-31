// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"
)

// loadDotEnvFiles populates the process environment from, in order of
// decreasing precedence:
//
//  1. variables already exported in the environment
//  2. ./.env in the working directory
//  3. ~/.config/notioncli/.env
//
// godotenv.Load never overwrites a variable that is already set, so simply
// loading both files in that order yields exactly that precedence.
//
// Both loads are best-effort. This used to gate the home-config load on the
// working-directory load having FAILED, which produced two defects (#99):
//
//   - With no .env anywhere, the CLI printed "Error loading .env file" and
//     called os.Exit(1) BEFORE looking at the environment — so an exported
//     NOTION_API_KEY was rejected before it was ever read, breaking every
//     CI, container and shell-export workflow the README documents.
//   - godotenv.Load returns nil for any parseable file, so an unrelated
//     ./.env — a Rails app's, say — satisfied the first load and the home
//     config was silently never read. The CLI then failed as unauthenticated
//     in a directory that merely happened to contain a dotfile.
//
// A missing or unreadable .env is not an error: it is the normal state for
// anyone who configures the tool through the environment.
func loadDotEnvFiles() {
	if wd, err := os.Getwd(); err == nil {
		_ = godotenv.Load(filepath.Join(wd, ".env"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		_ = godotenv.Load(filepath.Join(home, ".config", "notioncli", ".env"))
	}
}

// SetAPIConfig resolves the Notion credential and the optional default page.
//
// It returns an empty api key rather than exiting when no credential can be
// found; every caller already treats "" as ErrMissingAPIKey. A library must
// not call os.Exit — it gave the CLI no way to report the failure as JSON,
// no way for a test to exercise the path, and printed to stdout, where a
// --json consumer would have parsed it as data.
//
// NOTION_PAGE_ID is not required here: the persistent --page flag can supply
// the target, so resolvePageID in cmd/root.go owns that precedence.
func SetAPIConfig() (string, string) {
	loadDotEnvFiles()
	return os.Getenv("NOTION_API_KEY"), os.Getenv("NOTION_PAGE_ID")
}

// GetLocalTimeZone resolves LOCAL_TIMEZONE into a *time.Location.
//
// Unset is not fatal — it falls back to the host's local zone, which is what
// a user without configuration means. Previously this required a .env file
// to exist at all and hard-exited when none did, so `blocks list` was
// unusable on a machine configured purely through the environment.
func GetLocalTimeZone() (*time.Location, error) {
	loadDotEnvFiles()

	name, ok := os.LookupEnv("LOCAL_TIMEZONE")
	if !ok || name == "" {
		return time.Local, nil
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("LOCAL_TIMEZONE %q is not a valid IANA zone: %w", name, err)
	}
	return location, nil
}
