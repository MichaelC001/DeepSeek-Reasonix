package builtin

import (
	"encoding/json"
	"reasonix/internal/evidence"
	"strings"
	"testing"
)

func TestCompleteStepOptionalEvidenceDoesNotCreateQualityGate(t *testing.T) {
	for _, status := range []string{"pending", "in_progress", "completed"} {
		ctx := declaredStepContext(evidence.TodoItem{Content: "implement", Status: status})
		result, err := (completeStep{}).Execute(ctx, json.RawMessage(`{"step":"implement","result":"model says done"}`))
		if err != nil || !strings.Contains(result, "declaration") {
			t.Fatalf("status=%s result=%q error=%v", status, result, err)
		}
	}
}
