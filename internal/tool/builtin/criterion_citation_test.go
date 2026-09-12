package builtin

import (
	"encoding/json"
	"testing"

	"reasonix/internal/evidence"
)

func criterionCitationArgs(t *testing.T, criterionID string) json.RawMessage {
	t.Helper()
	args, err := json.Marshal(map[string]any{
		"step":   "fix it",
		"result": "the race is gone",
		"evidence": []map[string]any{
			{"kind": "manual", "summary": "walked the retry path", "criterion_id": criterionID},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return args
}

func TestCriterionCitationIsModelDeclarationOnly(t *testing.T) {
	for _, id := range []string{"c1", "c9"} {
		ctx := declaredStepContext(evidence.TodoItem{Content: "fix it", Status: "in_progress"})
		ctx = evidence.WithAcceptanceCriteria(ctx, []string{"c1", "c2"})
		if _, err := (completeStep{}).Execute(ctx, criterionCitationArgs(t, id)); err != nil {
			t.Fatal(err)
		}
	}
}
