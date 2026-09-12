package builtin

import (
	"encoding/json"
	"reasonix/internal/evidence"
	"testing"
)

func TestCompleteStepRejectsMissingOrAmbiguousTodo(t *testing.T) {
	for _, todos := range [][]evidence.TodoItem{nil, {{Content: "duplicate", Status: "pending"}, {Content: "duplicate", Status: "in_progress"}}} {
		if _, err := (completeStep{}).Execute(declaredStepContext(todos...), json.RawMessage(`{"step":"duplicate","result":"done"}`)); err == nil {
			t.Fatalf("accepted unmatched or ambiguous target: %+v", todos)
		}
	}
}
func TestCompleteStepCanDeclarePendingItemWithoutSerialGate(t *testing.T) {
	ctx := declaredStepContext(evidence.TodoItem{Content: "one", Status: "in_progress"}, evidence.TodoItem{Content: "two", Status: "pending"})
	if _, err := (completeStep{}).Execute(ctx, json.RawMessage(`{"step_index":2,"result":"done"}`)); err != nil {
		t.Fatal(err)
	}
}
