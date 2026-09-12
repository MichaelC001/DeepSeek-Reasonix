package agent

import (
	"testing"

	"reasonix/internal/evidence"
)

func readinessLedger(receipts ...evidence.Receipt) *evidence.Ledger {
	l := evidence.NewLedger()
	for _, r := range receipts {
		l.Record(r)
	}
	return l
}

func TestFinalReadinessAllowsIncompleteTodosInPlanMode(t *testing.T) {
	todo := evidence.Receipt{ToolName: "todo_write", Success: true, Todos: []evidence.TodoItem{{Content: "draft implementation plan", Status: "pending"}}}
	a := &Agent{task: taskRuntime{ledger: readinessLedger(todo)}}
	a.SetPlanMode(true)

	if got := a.ReadinessResult(); !got.Ready {
		t.Fatalf("ReadinessResult() = %+v, want ready in plan mode", got)
	}
	if got := a.ReadinessResult(); !got.Ready {
		t.Fatalf("finalReadinessCheckFor() applies in plan mode: %+v", got)
	}
}

func TestFinalReadinessLoopGuardPassSurvivesBookkeeping(t *testing.T) {
	todo := evidence.Receipt{ToolName: "todo_write", Success: true, Todos: []evidence.TodoItem{{Content: "edit", Status: "in_progress"}}}
	writer := evidence.Receipt{ToolName: "write_file", Success: true, Write: true, Paths: []string{"a.go"}}
	ledger := readinessLedger(writer, todo)
	a := &Agent{task: taskRuntime{ledger: ledger}, turn: turnRuntime{deliveryScopeActive: true}}
	a.armLoopGuardPass(ledger.Len())

	ledger.Record(evidence.Receipt{ToolName: "ask", Success: true})
	ledger.Record(evidence.Receipt{ToolName: "todo_write", Success: true, Todos: []evidence.TodoItem{{Content: "edit", Status: "in_progress"}}})
	ledger.Record(evidence.Receipt{ToolName: "complete_step", Success: true, Step: "edit"})

	if got := a.ReadinessResult(); got.Reason != "" {
		t.Fatalf("finalReadinessCheckFor() reason = %q, want bookkeeping after the guard to keep the pass", got.Reason)
	}
}

// TestFinalReadinessLoopGuardPassRevokedByRealProgress proves a successful
// write or command receipt after the guard revokes the pass: receipts are
// obtainable again, so readiness resumes enforcing them.
