package builtin

import (
	"encoding/json"
	"reasonix/internal/evidence"
	"reasonix/internal/instruction"
	"testing"
)

func TestCompleteStepProjectChecksRemainInstructions(t *testing.T) {
	ledger := evidence.NewLedger()
	ledger.Record(evidence.Receipt{ToolName: "edit_file", Paths: []string{"main.go"}, Write: true, Mutation: true, Success: true})
	ctx := evidence.WithLedger(declaredStepContext(evidence.TodoItem{Content: "implement", Status: "in_progress"}), ledger)
	ctx = instruction.WithChecks(ctx, []instruction.VerifyCheck{{Command: "go test ./...", SourcePath: "AGENTS.md"}})
	if _, err := (completeStep{}).Execute(ctx, json.RawMessage(`{"step":"implement","result":"done"}`)); err != nil {
		t.Fatal(err)
	}
	if ledger.HasSuccessfulCommand("go test ./...") {
		t.Fatal("missing project check was fabricated")
	}
}
