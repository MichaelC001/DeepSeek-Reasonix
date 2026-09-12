package builtin

import (
	"context"
	"encoding/json"
	"reasonix/internal/evidence"
	"strings"
	"testing"
)

func declaredStepContext(todos ...evidence.TodoItem) context.Context {
	return evidence.WithTodoState(context.Background(), todos)
}

func TestCompleteStepValidatesCompatibilityCalls(t *testing.T) {
	ctx := declaredStepContext(evidence.TodoItem{StepID: "s1", Content: "implement", Status: "pending"})
	for _, args := range []string{`{"step_id":"s1","result":"done"}`, `{"step":"implement","result":"done"}`, `{"step_index":1,"result":"done"}`} {
		result, err := (completeStep{}).Execute(ctx, json.RawMessage(args))
		if err != nil || !strings.Contains(result, "Model completion declaration") || strings.Contains(result, "host-verified") {
			t.Fatalf("result=%q err=%v", result, err)
		}
	}
	for _, args := range []string{`{`, `{}`, `{"step_id":"s1"}`, `{"step":"missing","result":"done"}`, `{"step_index":-1,"result":"done"}`, `{"step_id":"s1","result":"done","evidence":[{"kind":"invented","summary":"claim"}]}`} {
		if _, err := (completeStep{}).Execute(ctx, json.RawMessage(args)); err == nil {
			t.Errorf("accepted invalid call %s", args)
		}
	}
}

func TestCompleteStepRemainsHiddenAndReadOnly(t *testing.T) {
	tool := completeStep{}
	if tool.ProviderVisible(context.Background()) || !tool.ReadOnly() || tool.PlanModeSafe() {
		t.Fatal("compatibility entry changed discovery or Plan boundary")
	}
}
