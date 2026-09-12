package agent

import (
	"testing"

	"reasonix/internal/evidence"
)

func agentWithTodos(t *testing.T, todos []evidence.TodoItem) *Agent {
	t.Helper()
	a, _ := newEvidenceAgent(t, evidenceWriter{}, true)
	a.setTodoState(todos)
	return a
}

func TestOperationSuccessDoesNotCompleteTodos(t *testing.T) {
	a := agentWithTodos(t, []evidence.TodoItem{{Content: "Rewrite login.go", Status: "in_progress"}, {Content: "Update docs", Status: "pending"}})
	a.task.ledger.Record(evidence.Receipt{ToolName: "write_file", Success: true, Write: true, Paths: []string{"login.go"}})
	todos := a.CanonicalTodoState()
	if todos[0].Status != "in_progress" || todos[1].Status != "pending" {
		t.Fatalf("host changed progress: %+v", todos)
	}
}
