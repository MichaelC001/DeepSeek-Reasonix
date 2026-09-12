package builtin

import (
	"encoding/json"
	"reasonix/internal/evidence"
	"strings"
	"testing"
)

func TestCompleteStepNeverClaimsNextStepWasAdvanced(t *testing.T) {
	todos := []evidence.TodoItem{{Content: "one", Status: "in_progress"}, {Content: "two", Status: "pending"}}
	result, err := (completeStep{}).Execute(declaredStepContext(todos...), json.RawMessage(`{"step":"one","result":"finished"}`))
	if err != nil {
		t.Fatal(err)
	}
	if todos[1].Status != "pending" || strings.Contains(result, "advanced") || strings.Contains(result, "All steps completed") {
		t.Fatalf("inferred another item: %q %+v", result, todos)
	}
}
