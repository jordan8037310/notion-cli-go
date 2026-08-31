package utils

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
	// Isolate HOME too: the home config is now loaded unconditionally
	// rather than only when the cwd load failed (issue #99), so a real
	// ~/.config/notioncli/.env would otherwise supply the value this test
	// is trying to leave unset.
	t.Setenv("HOME", t.TempDir())

	// An unset LOCAL_TIMEZONE now falls back to the host's local zone
	// rather than erroring. Requiring it meant `blocks list` was unusable
	// on a machine configured purely through the environment, and "no
	// timezone configured" plainly means "use mine" (issue #99).
	if err := os.Unsetenv("LOCAL_TIMEZONE"); err != nil {
		t.Fatalf("Unsetenv: %v", err)
	}
	loc, err := GetLocalTimeZone()
	if err != nil {
		t.Fatalf("unset LOCAL_TIMEZONE should fall back, not error: %v", err)
	}
	if loc != time.Local {
		t.Errorf("fallback = %v, want time.Local", loc)
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

// TestSetAPIConfig_ExportedKeyIsEnough guards the headline half of issue
// #99. SetAPIConfig used to hard-exit with "Error loading .env file" when no
// .env existed anywhere — before it ever looked at the environment. That
// rejected an exported NOTION_API_KEY and broke every CI, container and
// shell-export workflow, all of which the README documents as supported.
func TestSetAPIConfig_ExportedKeyIsEnough(t *testing.T) {
	chdirTemp(t)
	t.Setenv("HOME", t.TempDir()) // no home config either
	t.Setenv("NOTION_API_KEY", "secret_exported")

	key, _ := SetAPIConfig()
	if key != "secret_exported" {
		t.Fatalf("api key = %q, want the exported value; an env-only setup must work with no .env at all", key)
	}
}

// TestSetAPIConfig_UnrelatedDotEnvDoesNotShadowHome guards the subtler half.
// godotenv.Load returns nil for ANY parseable file, so the old code — which
// only tried the home config when the working-directory load had failed —
// let an unrelated ./.env (a Rails app's, say) silently suppress the user's
// real credential.
func TestSetAPIConfig_UnrelatedDotEnvDoesNotShadowHome(t *testing.T) {
	dir := chdirTemp(t)
	// An unrelated .env: parses fine, says nothing about Notion.
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("DATABASE_URL=postgres://x\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".config", "notioncli"), 0o755); err != nil {
		t.Fatalf("mkdir home config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "notioncli", ".env"),
		[]byte("NOTION_API_KEY=secret_from_home\n"), 0o600); err != nil {
		t.Fatalf("write home config: %v", err)
	}
	os.Unsetenv("NOTION_API_KEY")

	if key, _ := SetAPIConfig(); key != "secret_from_home" {
		t.Fatalf("api key = %q, want secret_from_home; an unrelated ./.env must not shadow the home config", key)
	}
}

// TestSetAPIConfig_PrecedenceEnvOverFiles pins the order: an explicitly
// exported variable beats both files.
func TestSetAPIConfig_PrecedenceEnvOverFiles(t *testing.T) {
	dir := chdirTemp(t)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("NOTION_API_KEY=from_cwd\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NOTION_API_KEY", "from_environment")

	if key, _ := SetAPIConfig(); key != "from_environment" {
		t.Errorf("api key = %q, want the exported value to win over ./.env", key)
	}
}

// TestSetAPIConfig_NoCredentialReturnsEmpty confirms the library reports the
// failure instead of terminating the process. os.Exit from utils gave the
// CLI no way to render the error as JSON and no way for a test to reach the
// path at all.
func TestSetAPIConfig_NoCredentialReturnsEmpty(t *testing.T) {
	chdirTemp(t)
	t.Setenv("HOME", t.TempDir())
	os.Unsetenv("NOTION_API_KEY")

	if key, _ := SetAPIConfig(); key != "" {
		t.Errorf("api key = %q, want empty when nothing is configured", key)
	}
}

// chdirTemp moves into a fresh directory for the duration of a test and
// returns it.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	// t.TempDir on macOS hands back a /var symlink; resolve it so paths
	// compare equal to what Getwd reports inside the test.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}
