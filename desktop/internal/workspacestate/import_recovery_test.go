package workspacestate

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSettleImportedWorkspaceConflictMatchesOnlyPublishedSourceVersion(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "state.json"))
	mapping := SourceMapping{SourceKey: "source:head", Path: "/source.jsonl", HeadID: "head", Format: "legacy", Fingerprint: "version", SessionID: "imported", WorkspaceID: "global"}
	identity := State{SourceMappings: map[string]SourceMapping{mapping.SourceKey: mapping}}
	aliases := identity.SourceKeys(mapping.SourceKey)
	if len(aliases) != 2 {
		t.Fatalf("fixture requires a durable and normalized source identity: %v", aliases)
	}
	base := RecoveryEntry{SourceKey: mapping.SourceKey, Path: mapping.Path, HeadID: mapping.HeadID, Format: mapping.Format,
		Fingerprint: mapping.Fingerprint, Reason: "workspace_conflict", Status: "pending", WorkspaceRoot: "/previous-workspace"}
	variants := map[string]func(*RecoveryEntry){
		"matched":          func(e *RecoveryEntry) {},
		"normalized-alias": func(e *RecoveryEntry) { e.SourceKey = aliases[1] },
		"other-source":     func(e *RecoveryEntry) { e.SourceKey = "other-source:head" },
		"other-head-key":   func(e *RecoveryEntry) { e.SourceKey = "source:other-head"; e.HeadID = "other-head" },
		"other-head":       func(e *RecoveryEntry) { e.HeadID = "other-head" },
		"other-version":    func(e *RecoveryEntry) { e.Fingerprint = "new-version" },
		"unknown-version":  func(e *RecoveryEntry) { e.Fingerprint = "" },
		"other-format":     func(e *RecoveryEntry) { e.Format = "legacy-trash" },
		"changed-source":   func(e *RecoveryEntry) { e.Reason = "source_changed_after_adoption" },
		"alternate-head":   func(e *RecoveryEntry) { e.Reason = "alternate_head" },
		"unreadable":       func(e *RecoveryEntry) { e.Reason = "metadata_unreadable" },
		"already-restored": func(e *RecoveryEntry) { e.Status = "restored"; e.SessionID = "previous-target" },
	}
	if err := s.mutate(t.Context(), func(state *State) error {
		state.Workspaces["global"] = Workspace{ID: "global", Root: t.TempDir(), SessionIDs: []string{"imported"}}
		state.SessionStates["imported"] = SessionState{Lifecycle: Active}
		state.SourceMappings[mapping.SourceKey] = mapping
		for id, change := range variants {
			entry := base
			entry.ID = id
			change(&entry)
			state.RecoveryEntries[id] = entry
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SettleImportedWorkspaceConflict(t.Context(), mapping.SourceKey, mapping.Fingerprint, mapping.SessionID); err != nil {
		t.Fatal(err)
	}
	after, err := NewStore(s.path).Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for id, old := range before.RecoveryEntries {
		want := old
		if id == "matched" || id == "normalized-alias" {
			want.Status, want.SessionID = "restored", mapping.SessionID
		}
		if got := after.RecoveryEntries[id]; !reflect.DeepEqual(got, want) {
			t.Errorf("entry %s = %+v, want %+v", id, got, want)
		}
	}
	if err := s.SettleImportedWorkspaceConflict(t.Context(), mapping.SourceKey, mapping.Fingerprint, mapping.SessionID); err != nil {
		t.Fatal(err)
	}
	again, err := s.Load(t.Context())
	if err != nil || again.Generation != after.Generation {
		t.Fatalf("repeat changed settled state: generation %d -> %d, err=%v", after.Generation, again.Generation, err)
	}
	for _, args := range [][3]string{
		{"unpublished-source", mapping.Fingerprint, mapping.SessionID},
		{mapping.SourceKey, "changed-version", mapping.SessionID},
		{mapping.SourceKey, "", mapping.SessionID},
		{mapping.SourceKey, mapping.Fingerprint, "other-target"},
	} {
		if err := s.SettleImportedWorkspaceConflict(t.Context(), args[0], args[1], args[2]); !errors.Is(err, ErrMutationConflict) {
			t.Fatalf("unpublished identity %v accepted: %v", args, err)
		}
	}
	final, err := s.Load(t.Context())
	if err != nil || final.Generation != after.Generation {
		t.Fatalf("failed settlement changed registry: generation %d -> %d, err=%v", after.Generation, final.Generation, err)
	}
}

func TestSettleImportedWorkspaceConflictPreservesAmbiguousAlias(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "state.json"))
	first := SourceMapping{SourceKey: "old-key", Path: "/source.jsonl", HeadID: "head", Format: "legacy", Fingerprint: "version", SessionID: "first", WorkspaceID: "global"}
	second := first
	second.SourceKey, second.SessionID = "other-old-key", "second"
	identity := State{SourceMappings: map[string]SourceMapping{first.SourceKey: first, second.SourceKey: second}}
	alias := identity.SourceKeys(first.SourceKey)[1]
	if err := s.mutate(t.Context(), func(state *State) error {
		state.Workspaces["global"] = Workspace{ID: "global", Root: t.TempDir(), SessionIDs: []string{"first", "second"}}
		state.SessionStates["first"], state.SessionStates["second"] = SessionState{Lifecycle: Active}, SessionState{Lifecycle: Active}
		state.SourceMappings[first.SourceKey], state.SourceMappings[second.SourceKey] = first, second
		state.RecoveryEntries["ambiguous"] = RecoveryEntry{ID: "ambiguous", SourceKey: alias, HeadID: first.HeadID, Format: first.Format,
			Fingerprint: first.Fingerprint, Reason: "workspace_conflict", Status: "pending"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SettleImportedWorkspaceConflict(t.Context(), first.SourceKey, first.Fingerprint, first.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleImportedWorkspaceConflict(t.Context(), alias, first.Fingerprint, first.SessionID); !errors.Is(err, ErrMutationConflict) {
		t.Fatalf("ambiguous import identity accepted: %v", err)
	}
	after, err := s.Load(t.Context())
	if err != nil || after.Generation != before.Generation || after.RecoveryEntries["ambiguous"].Status != "pending" {
		t.Fatalf("ambiguous recovery was changed: %+v, err=%v", after.RecoveryEntries, err)
	}
}
