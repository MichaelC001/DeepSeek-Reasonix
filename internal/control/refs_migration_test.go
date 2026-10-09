package control

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionAuthorizationsPreservesExternalFolderBindingsWithoutAliasing(t *testing.T) {
	workspace := t.TempDir()
	original := newOwnedTestController(t, Options{WorkspaceRoot: workspace})
	root := externalMigrationRoot(t)
	token, display, err := original.RegisterExternalFolderRef(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := original.SessionAuthorizations()

	laterToken, _, err := original.RegisterExternalFolderRef(externalMigrationRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	registrar := &recordingExternalFolderToolRefs{}
	replacement := newOwnedTestController(t, Options{WorkspaceRoot: workspace, ExternalFolderToolRefs: registrar})
	approvalMode := replacement.ToolApprovalMode()
	replacement.RestoreSessionAuthorizations(snapshot)
	assertMigratedExternalRoot(t, replacement, token, display)
	if registrar.token != token || filepath.ToSlash(registrar.root) != display {
		t.Fatalf("read-tool registration = (%q, %q), want (%q, %q)", registrar.token, registrar.root, token, display)
	}
	if _, _, ok := replacement.ExternalFolderRefLocalPath(laterToken); ok {
		t.Fatal("source registration after snapshot leaked into replacement")
	}

	replacementOnly, _, err := replacement.RegisterExternalFolderRef(externalMigrationRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := original.ExternalFolderRefLocalPath(replacementOnly); ok {
		t.Fatal("replacement registration leaked back into source")
	}
	another := newOwnedTestController(t, Options{WorkspaceRoot: workspace})
	another.RestoreSessionAuthorizations(snapshot)
	assertMigratedExternalRoot(t, another, token, display)
	for _, unowned := range []string{laterToken, replacementOnly} {
		if _, _, ok := another.ExternalFolderRefLocalPath(unowned); ok {
			t.Fatalf("authorization snapshot aliased a controller's later registration %q", unowned)
		}
	}
	for _, ctrl := range []*Controller{original, replacement, another} {
		auth := ctrl.SessionAuthorizations()
		if len(auth.Grants) != 0 || len(auth.PlanModeReadOnlyCommands) != 0 || len(auth.WriteRoots) != 0 {
			t.Fatal("external-folder read authorization widened tool or write grants")
		}
	}
	if replacement.ToolApprovalMode() != approvalMode {
		t.Fatal("external-folder restore changed the permission preset")
	}
}

func TestRestoreSessionAuthorizationsKeepsExistingExternalFolderBindings(t *testing.T) {
	workspace := t.TempDir()
	original := newOwnedTestController(t, Options{WorkspaceRoot: workspace})
	oldToken, oldDisplay, err := original.RegisterExternalFolderRef(externalMigrationRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	replacement := newOwnedTestController(t, Options{WorkspaceRoot: workspace})
	newToken, newDisplay, err := replacement.RegisterExternalFolderRef(externalMigrationRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	replacement.RestoreSessionAuthorizations(original.SessionAuthorizations())
	replacement.RestoreSessionAuthorizations(SessionAuthorizations{})
	assertMigratedExternalRoot(t, replacement, oldToken, oldDisplay)
	assertMigratedExternalRoot(t, replacement, newToken, newDisplay)
}

func externalMigrationRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Dropped Folder")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func assertMigratedExternalRoot(t *testing.T, ctrl *Controller, token, display string) {
	t.Helper()
	path, gotDisplay, ok := ctrl.ExternalFolderRefLocalPath(token)
	if !ok || filepath.ToSlash(path) != display || gotDisplay != display {
		t.Fatalf("original token %q resolves to (%q, %q, %v), want %q", token, path, gotDisplay, ok, display)
	}
	if _, _, ok := ctrl.ExternalFolderRefLocalPath(token + "/../outside.txt"); ok {
		t.Fatal("restored reference permits traversal outside its registered root")
	}
}
