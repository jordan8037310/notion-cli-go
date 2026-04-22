package utils

import (
	"os"
	"path/filepath"
	"testing"
)

// These tests exercise the happy paths of SetAPIConfig and GetLocalTimeZone.
// The error branches call os.Exit(1) unconditionally on missing env/.env,
// which is not testable without refactoring the production code. Fixing
// that is tracked separately (see issue #1 — utils client refactor).

func TestSetAPIConfig(t *testing.T) {
	// Neutralize .env discovery: point cwd at an empty tmp dir and HOME at
	// another empty tmp dir. If a .env ever existed at either location it
	// would be loaded; with both empty, godotenv.Load fails, but SetAPIConfig
	// then falls through to os.LookupEnv which succeeds because we set both
	// required vars here.
	emptyCwd := t.TempDir()
	emptyHome := t.TempDir()

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(emptyCwd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	t.Setenv("HOME", emptyHome)
	t.Setenv("NOTION_API_KEY", "sk_test")
	t.Setenv("NOTION_PAGE_ID", "page_test")

	// godotenv.Load will fail on both candidate paths; SetAPIConfig keeps
	// going as long as the env vars are set in the process environment.
	// Because both paths fail, SetAPIConfig would os.Exit — so we need at
	// least one .env to exist to reach the return statement. Create an
	// empty one in cwd.
	envPath := filepath.Join(emptyCwd, ".env")
	if err := os.WriteFile(envPath, []byte(""), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	key, page := SetAPIConfig()
	if key != "sk_test" {
		t.Errorf("key=%q want sk_test", key)
	}
	if page != "page_test" {
		t.Errorf("page=%q want page_test", page)
	}
}

func TestGetLocalTimeZone(t *testing.T) {
	emptyCwd := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(emptyCwd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	envPath := filepath.Join(emptyCwd, ".env")
	if err := os.WriteFile(envPath, []byte(""), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	t.Setenv("LOCAL_TIMEZONE", "America/New_York")
	loc, err := GetLocalTimeZone()
	if err != nil {
		t.Fatalf("GetLocalTimeZone: %v", err)
	}
	if loc.String() != "America/New_York" {
		t.Errorf("loc=%s want America/New_York", loc)
	}
}

func TestGetLocalTimeZoneMissingEnv(t *testing.T) {
	emptyCwd := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(emptyCwd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	envPath := filepath.Join(emptyCwd, ".env")
	if err := os.WriteFile(envPath, []byte(""), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	// Unset to make the lookup fail.
	if err := os.Unsetenv("LOCAL_TIMEZONE"); err != nil {
		t.Fatalf("Unsetenv: %v", err)
	}
	if _, err := GetLocalTimeZone(); err == nil {
		t.Fatal("expected error when LOCAL_TIMEZONE unset, got nil")
	}
}

func TestGetLocalTimeZoneBadValue(t *testing.T) {
	emptyCwd := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(emptyCwd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	envPath := filepath.Join(emptyCwd, ".env")
	if err := os.WriteFile(envPath, []byte(""), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	t.Setenv("LOCAL_TIMEZONE", "Not/A/Zone")
	if _, err := GetLocalTimeZone(); err == nil {
		t.Fatal("expected error for invalid timezone, got nil")
	}
}
