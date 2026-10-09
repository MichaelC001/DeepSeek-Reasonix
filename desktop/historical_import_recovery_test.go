package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"reasonix/desktop/internal/workspacestate"
	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

func TestHistoricalImportRetrySettlesWorkspaceConflict(t *testing.T) {
	app, projectRoot, path := historicalRecoveryFixture(t)
	app.ctx = t.Context()
	installNoopRuntimeEvents(app)
	t.Cleanup(app.stopHistoricalImports)
	elsewhere := historicalRecoveryGitRoot(t, robustTempDir(t))
	if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{Scope: "project", WorkspaceRoot: elsewhere}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ListHistoricalSessions(); err != nil {
		t.Fatal(err)
	}
	if _, err := app.StartHistoricalImport(nil); err != nil {
		t.Fatal(err)
	}
	first := awaitHistoricalBatch(t, app)
	if len(first.Items) != 1 || first.Items[0].Status != "failed" || first.Items[0].ErrorCode != "workspace_conflict" {
		t.Fatalf("fixture must create actual conflict: %+v", first)
	}
	before, err := app.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(before.RecoveryEntries) != 1 {
		t.Fatalf("conflict did not create one recovery entry: %+v", before.RecoveryEntries)
	}
	if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{Scope: "project", WorkspaceRoot: projectRoot}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.StartHistoricalImport(nil); err != nil {
		t.Fatal(err)
	}
	after := awaitHistoricalBatch(t, app)
	if len(after.Items) != 1 || after.Items[0].Status != "imported" {
		t.Fatalf("retry failed: %+v", after)
	}
	state, err := app.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	histories := v5MigrationHistories(t, app)
	if len(histories) != 1 {
		t.Fatalf("retry did not produce readable history: %+v", histories)
	}
	pending := 0
	for _, entry := range state.RecoveryEntries {
		if entry.Status == "pending" {
			pending++
		}
	}
	t.Logf("actual workspace_conflict -> repaired metadata -> imported + readable; recovery entries still pending=%d", pending)
	if pending != 0 {
		t.Errorf("successful retry left %d stale pending recovery entries", pending)
	}
}

func TestHistoricalImportReceiptReplaySettlesWorkspaceConflict(t *testing.T) {
	app, projectRoot, path := historicalRecoveryFixture(t)
	source := desktopMigrationSource{scope: "project", workspaceRoot: projectRoot}
	if err := app.migrateLegacySession(t.Context(), path, source, ""); err != nil {
		t.Fatal(err)
	}
	state, err := app.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.SourceMappings) != 1 {
		t.Fatalf("fixture did not publish exactly one source: %+v", state.SourceMappings)
	}
	var mapping workspacestate.SourceMapping
	for _, imported := range state.SourceMappings {
		mapping = imported
	}
	if got := v5MigrationHistories(t, app); len(got) != 1 {
		t.Fatalf("fixture did not import readable history: %+v", got)
	}
	// Simulate a receipt written by an earlier build, which imported successfully
	// but left its exact source-version conflict pending.
	if err := app.sourceRecovery(t.Context(), path, "legacy", "workspace_conflict", "project", projectRoot, mapping.HeadID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := app.migrateLegacySession(t.Context(), path, source, ""); err != nil {
			t.Fatal(err)
		}
		state, err := app.workspaceRegistry().Load(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(state.RecoveryEntries) != 1 {
			t.Fatalf("unexpected recovery entries: %+v", state.RecoveryEntries)
		}
		for _, entry := range state.RecoveryEntries {
			if entry.Status != "restored" || entry.SessionID == "" {
				t.Fatalf("completed receipt left stale recovery: %+v", entry)
			}
		}
		if got := v5MigrationHistories(t, app); len(got) != 1 {
			t.Fatalf("receipt replay duplicated or lost history: %+v", got)
		}
	}
}

func TestHistoricalImportPartialRetryPreservesOtherConflict(t *testing.T) {
	app, firstRoot, firstPath := historicalRecoveryFixture(t)
	secondRoot, secondPath := historicalRecoveryLegacySource(t)
	elsewhere := historicalRecoveryGitRoot(t, robustTempDir(t))
	app.ctx = t.Context()
	installNoopRuntimeEvents(app)
	t.Cleanup(app.stopHistoricalImports)
	for _, path := range []string{firstPath, secondPath} {
		if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{Scope: "project", WorkspaceRoot: elsewhere}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.ListHistoricalSessions(); err != nil {
		t.Fatal(err)
	}
	if _, err := app.StartHistoricalImport(nil); err != nil {
		t.Fatal(err)
	}
	first := awaitHistoricalBatch(t, app)
	if len(first.Items) != 2 || first.Failed != 2 {
		t.Fatalf("expected two real conflicts: %+v", first)
	}
	for index, repair := range []struct{ root, path string }{{firstRoot, firstPath}, {secondRoot, secondPath}} {
		if err := agent.SaveBranchMetaPreserveUpdated(repair.path, agent.BranchMeta{Scope: "project", WorkspaceRoot: repair.root}); err != nil {
			t.Fatal(err)
		}
		if _, err := app.StartHistoricalImport(nil); err != nil {
			t.Fatal(err)
		}
		batch := awaitHistoricalBatch(t, app)
		if batch.Failed != 1-index {
			t.Fatalf("retry failed unexpected sources: %+v", batch)
		}
		state, err := app.workspaceRegistry().Load(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		pending := 0
		for _, entry := range state.RecoveryEntries {
			if entry.Status == "pending" {
				pending++
				if entry.SourceKey != desktopSourceKey(secondPath, "") {
					t.Fatalf("wrong conflict remains pending: %+v", entry)
				}
			}
		}
		if pending != 1-index {
			t.Fatalf("retry settled an unresolved sibling or kept repaired conflict: pending=%d", pending)
		}
		if got := v5MigrationHistories(t, app); len(got) != index+1 {
			t.Fatalf("partial retry histories: %+v", got)
		}
	}
	if _, err := app.StartHistoricalImport(nil); err != nil {
		t.Fatal(err)
	}
	if batch := awaitHistoricalBatch(t, app); batch.Failed != 0 {
		t.Fatalf("repeat failed: %+v", batch)
	}
	if got := v5MigrationHistories(t, app); len(got) != 2 {
		t.Fatalf("repeat duplicated or lost history: %+v", got)
	}
}

// Use independent repositories so a runner's enclosing worktree cannot fold
// unrelated fixture projects or source directories into the same identity.
func historicalRecoveryGitRoot(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "--quiet", dir).CombinedOutput(); err != nil {
		t.Fatalf("initialize fixture repository: %v: %s", err, out)
	}
	if !sameDesktopPath(canonicalRuntimeRoot(dir), dir) {
		t.Fatalf("fixture root collapsed: %q -> %q", dir, canonicalRuntimeRoot(dir))
	}
	return dir
}

func historicalRecoveryFixture(t *testing.T) (*App, string, string) {
	t.Helper()
	isolateDesktopUserDirs(t)
	historicalRecoveryGitRoot(t, globalWorkspaceRoot())
	projectRoot, path := historicalRecoveryLegacySource(t)
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	app.desktopSessions.root = historicalRecoveryGitRoot(t, filepath.Join(t.TempDir(), "by-id"))
	app.desktopSessions.workspaceState = workspacestate.NewStore(filepath.Join(t.TempDir(), "workspace-state-v1.json"))
	return app, projectRoot, path
}

func historicalRecoveryLegacySource(t *testing.T) (string, string) {
	t.Helper()
	projectRoot := historicalRecoveryGitRoot(t, robustTempDir(t))
	if err := addProject(projectRoot, "Legacy project"); err != nil {
		t.Fatal(err)
	}
	dir := historicalRecoveryGitRoot(t, desktopSessionDir(projectRoot))
	path := filepath.Join(dir, "legacy.jsonl")
	legacy := agent.NewSession("system")
	legacy.Add(provider.Message{ID: "user", Role: provider.RoleUser, Content: "legacy content"})
	if err := legacy.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{Scope: "project", WorkspaceRoot: projectRoot}); err != nil {
		t.Fatal(err)
	}
	return projectRoot, path
}
