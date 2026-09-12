package agent

import (
	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/tool"
	"testing"
)

func TestCompletionReceiptReflectsObservedCheckResults(t *testing.T) {
	a := New(nil, tool.NewRegistry(), NewSession(""), Options{}, event.Discard)
	a.resetTurnEvidence()
	a.task.ledger.Record(evidence.Receipt{ToolName: "bash", Command: "go test ./...", Success: false})
	first := a.CompletionReceipt()
	if first == nil || first.AssessmentKind != "facts" || first.Verifications[0].Passed {
		t.Fatalf("first=%+v", first)
	}
	a.task.ledger.Record(evidence.Receipt{ToolName: "bash", Command: "go test ./...", Success: true})
	second := a.CompletionReceipt()
	if !second.Verifications[0].Passed || first.Verifications[0].Passed {
		t.Fatal("snapshot or actual outcome was lost")
	}
}
