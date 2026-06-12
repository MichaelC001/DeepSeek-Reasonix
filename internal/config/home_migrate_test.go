package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func migrateHomeForTest(t *testing.T, from, to string, apply bool) *HomeMigrationReport {
	t.Helper()
	report, err := MigrateHome(HomeMigrationOptions{
		From:  from,
		To:    to,
		Apply: apply,
		RunID: "test-run",
	})
	if err != nil {
		t.Fatalf("MigrateHome: %v", err)
	}
	return report
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestMigrateHomeDryRunDoesNotWriteDestination(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "old")
	to := filepath.Join(root, "new")
	writePathTestFile(t, filepath.Join(from, "config.toml"), `default_model = "old"`)
	writePathTestFile(t, filepath.Join(from, "sessions", "chat.jsonl"), `{"type":"user"}`)

	report := migrateHomeForTest(t, from, to, false)

	if !report.Needed {
		t.Fatal("Needed = false, want true")
	}
	if report.FilesCopied == 0 || report.DirsCreated == 0 {
		t.Fatalf("report did not preview copies: %+v", report)
	}
	if _, err := os.Stat(to); !os.IsNotExist(err) {
		t.Fatalf("destination was created during dry-run: %v", err)
	}
}

func TestMigrateHomeApplyCopiesDataAndWritesMarker(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "old")
	to := filepath.Join(root, "new")
	writePathTestFile(t, filepath.Join(from, "config.toml"), `default_model = "old"`)
	writePathTestFile(t, filepath.Join(from, "credentials"), "KEY_OLD=1\n")
	writePathTestFile(t, filepath.Join(from, "desktop-workspace"), "/tmp/work\n")
	writePathTestFile(t, filepath.Join(from, "projects", "proj", "sessions", "chat.jsonl"), `{"type":"user"}`)

	report := migrateHomeForTest(t, from, to, true)

	if !report.Needed || report.Marker == "" {
		t.Fatalf("report = %+v", report)
	}
	if got := readTestFile(t, filepath.Join(to, "desktop-workspace")); got != "/tmp/work\n" {
		t.Fatalf("desktop-workspace = %q", got)
	}
	if got := readTestFile(t, filepath.Join(to, "projects", "proj", "sessions", "chat.jsonl")); got != `{"type":"user"}` {
		t.Fatalf("project session = %q", got)
	}
	if _, err := os.Stat(HomeMigrationMarkerPath(to)); err != nil {
		t.Fatalf("marker missing: %v", err)
	}
}

func TestMigrateHomeMergesConfigWithDestinationPriority(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "old")
	to := filepath.Join(root, "new")
	writePathTestFile(t, filepath.Join(from, "config.toml"), `
default_model = "old"

[[plugins]]
name = "old-only"
command = "old-cmd"

[[plugins]]
name = "shared"
command = "old-shared"
`)
	writePathTestFile(t, filepath.Join(to, "config.toml"), `
default_model = "new"

[[plugins]]
name = "shared"
command = "new-shared"
`)

	report := migrateHomeForTest(t, from, to, true)

	if report.ConfigsMerged != 1 {
		t.Fatalf("ConfigsMerged = %d, want 1", report.ConfigsMerged)
	}
	cfg := Default()
	if err := mergeFile(cfg, filepath.Join(to, "config.toml")); err != nil {
		t.Fatalf("merge merged config: %v", err)
	}
	if cfg.DefaultModel != "new" {
		t.Fatalf("DefaultModel = %q, want new", cfg.DefaultModel)
	}
	plugins, err := mergeTOMLPlugins([]string{filepath.Join(to, "config.toml")})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]PluginEntry{}
	for _, p := range plugins {
		byName[p.Name] = p
	}
	if byName["old-only"].Command != "old-cmd" {
		t.Fatalf("old-only plugin missing: %+v", byName)
	}
	if byName["shared"].Command != "new-shared" {
		t.Fatalf("shared command = %q, want destination", byName["shared"].Command)
	}
	if _, err := os.Stat(filepath.Join(to, homeMigrationBackupDirName, "test-run", "config.source.toml")); err != nil {
		t.Fatalf("source config backup missing: %v", err)
	}
}

func TestMigrateHomeMergesCredentialsWithDestinationPriority(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "old")
	to := filepath.Join(root, "new")
	writePathTestFile(t, filepath.Join(from, "credentials"), "KEY_SHARED=old\nKEY_OLD=1\n")
	writePathTestFile(t, filepath.Join(to, "credentials"), "KEY_SHARED=new\nKEY_NEW=1\n")

	report := migrateHomeForTest(t, from, to, true)

	if report.CredentialsMerged != 1 {
		t.Fatalf("CredentialsMerged = %d, want 1", report.CredentialsMerged)
	}
	got := readTestFile(t, filepath.Join(to, "credentials"))
	if !strings.Contains(got, "KEY_SHARED=new\n") || !strings.Contains(got, "KEY_NEW=1\n") || !strings.Contains(got, "KEY_OLD=1\n") {
		t.Fatalf("merged credentials missing expected keys:\n%s", got)
	}
	if strings.Contains(got, "KEY_SHARED=old") {
		t.Fatalf("source shared key should not override destination:\n%s", got)
	}
}

func TestMigrateHomeRenamesSessionConflictsAndRemapsSidecars(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "old")
	to := filepath.Join(root, "new")
	writePathTestFile(t, filepath.Join(from, "sessions", "chat.jsonl"), "old session\n")
	writePathTestFile(t, filepath.Join(from, "sessions", "chat.jsonl.meta"), "old meta\n")
	writePathTestFile(t, filepath.Join(from, "sessions", "chat.ckpt", "state"), "old checkpoint\n")
	writePathTestFile(t, filepath.Join(from, "sessions", ".titles.json"), `{"chat.jsonl":"Old title"}`)
	writePathTestFile(t, filepath.Join(from, "sessions", ".display.json"), `{"chat.jsonl":{"abc":"Old display"}}`)
	writePathTestFile(t, filepath.Join(to, "sessions", "chat.jsonl"), "new session\n")
	writePathTestFile(t, filepath.Join(to, "sessions", ".titles.json"), `{"chat.jsonl":"New title"}`)

	report := migrateHomeForTest(t, from, to, true)

	if report.SessionConflictsRenamed != 1 {
		t.Fatalf("SessionConflictsRenamed = %d, want 1", report.SessionConflictsRenamed)
	}
	entries, err := os.ReadDir(filepath.Join(to, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	var migrated string
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".migrated-test-run") && strings.HasSuffix(entry.Name(), ".jsonl") {
			migrated = entry.Name()
			break
		}
	}
	if migrated == "" {
		t.Fatalf("renamed session not found in entries: %v", entries)
	}
	if got := readTestFile(t, filepath.Join(to, "sessions", migrated)); got != "old session\n" {
		t.Fatalf("renamed session body = %q", got)
	}
	if got := readTestFile(t, filepath.Join(to, "sessions", migrated+".meta")); got != "old meta\n" {
		t.Fatalf("renamed meta = %q", got)
	}
	ckpt := strings.TrimSuffix(migrated, ".jsonl") + ".ckpt"
	if got := readTestFile(t, filepath.Join(to, "sessions", ckpt, "state")); got != "old checkpoint\n" {
		t.Fatalf("renamed checkpoint = %q", got)
	}
	var titles map[string]string
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(to, "sessions", ".titles.json"))), &titles); err != nil {
		t.Fatal(err)
	}
	if titles["chat.jsonl"] != "New title" || titles[migrated] != "Old title" {
		t.Fatalf("titles = %#v", titles)
	}
	var displays map[string]map[string]string
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(to, "sessions", ".display.json"))), &displays); err != nil {
		t.Fatal(err)
	}
	if displays[migrated]["abc"] != "Old display" {
		t.Fatalf("displays = %#v", displays)
	}
}

func TestMigrateHomeArchivesUnknownConflicts(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "old")
	to := filepath.Join(root, "new")
	writePathTestFile(t, filepath.Join(from, "cache", "state.json"), "old cache\n")
	writePathTestFile(t, filepath.Join(to, "cache", "state.json"), "new cache\n")

	report := migrateHomeForTest(t, from, to, true)

	if report.ConflictsArchived != 1 {
		t.Fatalf("ConflictsArchived = %d, want 1", report.ConflictsArchived)
	}
	if got := readTestFile(t, filepath.Join(to, "cache", "state.json")); got != "new cache\n" {
		t.Fatalf("destination conflict was overwritten: %q", got)
	}
	archived := filepath.Join(to, homeMigrationConflictDirName, "test-run", "cache", "state.json")
	if got := readTestFile(t, archived); got != "old cache\n" {
		t.Fatalf("archived conflict = %q", got)
	}
}

func TestMigrateHomeMarkerMakesApplyIdempotent(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "old")
	to := filepath.Join(root, "new")
	writePathTestFile(t, filepath.Join(from, "sessions", "chat.jsonl"), "old session\n")

	first := migrateHomeForTest(t, from, to, true)
	if first.AlreadyMigrated {
		t.Fatal("first migration unexpectedly already migrated")
	}
	second := migrateHomeForTest(t, from, to, true)
	if !second.AlreadyMigrated {
		t.Fatalf("second migration AlreadyMigrated = false: %+v", second)
	}
	if second.FilesCopied != 0 || second.ConflictsArchived != 0 {
		t.Fatalf("second migration should be a no-op: %+v", second)
	}
}

func TestMigrateHomeRejectsNestedRoots(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "old")
	to := filepath.Join(from, "new")
	writePathTestFile(t, filepath.Join(from, "sessions", "chat.jsonl"), "old session\n")

	if _, err := MigrateHome(HomeMigrationOptions{From: from, To: to}); err == nil {
		t.Fatal("MigrateHome accepted destination nested under source")
	}
}

func TestMacOSCompletedHomeMigrationRootSwitchesStateRoot(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific path policy")
	}
	home := isolateUserPathEnv(t)
	native := filepath.Join(home, "Library", "Application Support", "reasonix")
	documented := filepath.Join(home, ".config", "reasonix")
	writePathTestFile(t, filepath.Join(native, "sessions", "old.jsonl"), "old\n")

	migrateHomeForTest(t, native, documented, true)

	if got := UserStateRoot(); got != documented {
		t.Fatalf("UserStateRoot() = %q, want %q", got, documented)
	}
	if roots := UserConfigRoots(); len(roots) != 1 || roots[0] != documented {
		t.Fatalf("UserConfigRoots() = %v, want [%s]", roots, documented)
	}
	if got := SessionDir(); got != filepath.Join(documented, "sessions") {
		t.Fatalf("SessionDir() = %q", got)
	}
}
