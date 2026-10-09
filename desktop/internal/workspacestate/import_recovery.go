package workspacestate

import (
	"context"
	"slices"
)

// SettleImportedWorkspaceConflict retires only the workspace-conflict report
// for the exact legacy source version that was successfully published. Metadata
// repairs can change workspace ownership without changing durable source bytes;
// other heads, source versions and recovery reasons still require their own proof.
func (s *Store) SettleImportedWorkspaceConflict(ctx context.Context, sourceKey, fingerprint, sessionID string) error {
	return s.mutate(ctx, func(state *State) error {
		mapping, exists, err := state.ResolveSource(sourceKey)
		if err != nil {
			return err
		}
		if !exists || fingerprint == "" || sessionID == "" || mapping.Fingerprint != fingerprint || mapping.SessionID != sessionID {
			return ErrMutationConflict
		}
		if mapping.Format != "legacy" || state.SessionStates[sessionID].Lifecycle == Deleted {
			return nil
		}
		if owner, exists := sessionOwner(*state, sessionID); !exists || owner != mapping.WorkspaceID {
			return ErrMutationConflict
		}
		keys := state.SourceKeys(mapping.SourceKey)
		for id, entry := range state.RecoveryEntries {
			if entry.Status != "pending" || entry.Reason != "workspace_conflict" || entry.Format != mapping.Format ||
				!slices.Contains(keys, entry.SourceKey) || entry.HeadID != mapping.HeadID || entry.Fingerprint != fingerprint {
				continue
			}
			owner, exists, err := state.ResolveSource(entry.SourceKey)
			if err != nil || !exists || owner.SessionID != sessionID || owner.WorkspaceID != mapping.WorkspaceID || owner.Fingerprint != fingerprint {
				continue
			}
			entry.Status, entry.SessionID = "restored", sessionID
			state.RecoveryEntries[id] = entry
		}
		return nil
	})
}
