package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspacePanelListsGenericTopLevelDirectories(t *testing.T) {
	base := t.TempDir()
	for _, dir := range []string{"tmp", "bin", "stage", "src", "node_modules", "dist"} {
		if err := os.MkdirAll(filepath.Join(base, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "main.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	listed := map[string]bool{}
	for _, e := range listDirForWorkspaceTarget(base, nil, "") {
		listed[e.Name] = true
	}
	for _, want := range []string{"tmp", "bin", "stage", "src", "main.py"} {
		if !listed[want] {
			t.Errorf("file panel hides %q, a real workspace entry; listed %v", want, listed)
		}
	}
	for _, hidden := range []string{"node_modules", "dist"} {
		if listed[hidden] {
			t.Errorf("file panel lists %q, which stays hidden as vendor/build output", hidden)
		}
	}
}

func TestWorkspaceWatchSeesChangesUnderGenericTopLevelDirectories(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"tmp/notes.py", "bin/run.sh", "stage/plan.md"} {
		if workspaceWatchPathSkipped(root, filepath.Join(root, filepath.FromSlash(rel))) {
			t.Errorf("a change to %s is dropped although the file panel shows it", rel)
		}
	}
	if !workspaceWatchPathSkipped(root, filepath.Join(root, "node_modules", "x", "index.js")) {
		t.Error("changes under node_modules must stay ignored")
	}
}
