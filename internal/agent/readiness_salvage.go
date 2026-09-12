package agent

import (
	"strings"

	"reasonix/internal/provider"
)

// currentFinalAssistantAnswer returns visible Content from the current turn.
// It accepts a plain final or the answer immediately before the paired
// update_goal error batch handled without another model round.
func currentFinalAssistantAnswer(sess *Session) string {
	if sess == nil {
		return ""
	}
	msgs := sess.Snapshot()
	if len(msgs) == 0 {
		return ""
	}
	last := msgs[len(msgs)-1]
	if last.Role == provider.RoleAssistant {
		if last.LocalOnly || len(last.ToolCalls) > 0 || strings.TrimSpace(last.Content) == "" {
			return ""
		}
		return last.Content
	}

	resultStart := len(msgs)
	for resultStart > 0 && msgs[resultStart-1].Role == provider.RoleTool {
		resultStart--
	}
	if resultStart == len(msgs) || resultStart == 0 {
		return ""
	}
	assistant := msgs[resultStart-1]
	if assistant.Role != provider.RoleAssistant || assistant.LocalOnly || strings.TrimSpace(assistant.Content) == "" ||
		!updateGoalResultsExactlyMatch(assistant.ToolCalls, msgs[resultStart:]) {
		return ""
	}
	return assistant.Content
}

func updateGoalResultsExactlyMatch(calls []provider.ToolCall, results []provider.Message) bool {
	if len(calls) == 0 || len(calls) != len(results) {
		return false
	}
	seen := make(map[string]struct{}, len(calls))
	for i, call := range calls {
		result := results[i]
		if call.ID == "" || call.Name != "update_goal" || result.Role != provider.RoleTool || result.LocalOnly ||
			result.ToolCallID != call.ID || result.Name != call.Name {
			return false
		}
		if _, duplicate := seen[call.ID]; duplicate {
			return false
		}
		seen[call.ID] = struct{}{}
	}
	return true
}
