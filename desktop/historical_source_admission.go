package main

import (
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/control"
)

const sessionOperationHistoricalSourcePending = "historical_source_pending"

// nativeHistoricalSource names the transcript a native legacy runtime continues
// in place. That runtime has no durable submission store, so the source must
// be prepared into a canonical session before it can accept an identified send.
func nativeHistoricalSource(ctrl control.SessionAPI, path, headID string) *SessionSourceRef {
	native, ok := ctrl.(*control.Controller)
	path = strings.TrimSpace(path)
	if !ok || path == "" || !native.NativeLegacySession() {
		return nil
	}
	path = agent.CanonicalSessionPath(path)
	return &SessionSourceRef{HostID: localDesktopHostID, Path: path, HeadID: headID, SourceKey: desktopSourceKey(path, headID)}
}

// tabHistoricalSourceLocked requires a.mu; it reports a source whether the tab
// was restored as a preparation shell or runs the source natively.
func tabHistoricalSourceLocked(tab *WorkspaceTab) *SessionSourceRef {
	if tab == nil {
		return nil
	}
	if tab.HistoricalSource != nil {
		return tab.HistoricalSource
	}
	return nativeHistoricalSource(tab.Ctrl, tab.currentSessionPath(), tab.SessionHeadID)
}

// identifiedSubmissionCheck runs under the turn admission locks, so the
// refusal names the same runtime the submission would have reached.
func identifiedSubmissionCheck(submissionID []string) func(control.SessionAPI) error {
	if firstSubmissionID(submissionID) == "" {
		return nil
	}
	return func(ctrl control.SessionAPI) error {
		if nativeHistoricalSource(ctrl, ctrl.SessionPath(), "") == nil {
			return nil
		}
		return newSessionOperationError(sessionOperationHistoricalSourcePending,
			"This conversation is still a historical source. Prepare it as a session before sending new messages.")
	}
}
