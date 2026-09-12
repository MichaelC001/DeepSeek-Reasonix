package agent

import (
	"encoding/json"
	"testing"

	"reasonix/internal/evidence"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func mutationPlanAndReceipt(path string) (*toolCallPlan, evidence.Receipt) {
	args := `{"path":"` + path + `"}`
	plan := &toolCallPlan{call: provider.ToolCall{Name: "write_file", Arguments: args}}
	rec := evidence.Receipt{
		ToolName:    "write_file",
		Success:     true,
		Write:       true,
		Mutation:    true,
		Paths:       []string{path},
		OperationID: evidence.OperationID("write_file", json.RawMessage(args)),
	}
	return plan, rec
}

func TestOrdinaryMutationSettlesOnTheRealResult(t *testing.T) {
	a, _ := newEvidenceAgent(t, evidenceWriter{}, true)
	plan, rec := mutationPlanAndReceipt("internal/auth/login.go")

	a.recordOperationOutcome(plan, rec, nil)

	op, ok := a.operations().Get(rec.OperationID)
	if !ok || op.State != evidence.OperationSettled {
		t.Fatalf("operation = %+v, want settled without a verification chore", op)
	}
	if gaps := a.readinessOperationGaps(); len(gaps) != 0 {
		t.Fatalf("ordinary work produced a delivery gap: %+v", gaps)
	}
}

func TestRetiredDeliveryMutationSettlesImmediately(t *testing.T) {
	a, ledger := newEvidenceAgent(t, evidenceWriter{}, true)
	plan, rec := mutationPlanAndReceipt("internal/auth/login.go")

	a.recordOperationOutcome(plan, rec, nil)
	op, _ := a.operations().Get(rec.OperationID)
	if op.State != evidence.OperationSettled {
		t.Fatalf("state = %q, want settled", op.State)
	}
	gaps := a.readinessOperationGaps()
	if len(gaps) != 0 {
		t.Fatalf("retired delivery stamp produced operation gaps: %+v", gaps)
	}

	// A recognized verifier that covers the changed file settles it — by path,
	// not by matching the command text the model would have had to retype.
	verify := evidence.Receipt{
		ToolName: "bash", Success: true, Command: "go test ./internal/auth",
		Paths:       []string{"internal/auth/login.go"},
		OperationID: evidence.OperationID("bash", json.RawMessage(`{"command":"go test ./internal/auth"}`)),
	}
	ledger.Record(verify)
	a.recordOperationOutcome(&toolCallPlan{call: provider.ToolCall{Name: "bash", Arguments: `{"command":"go test ./internal/auth"}`}}, verify, nil)

	if op, _ := a.operations().Get(rec.OperationID); op.State != evidence.OperationSettled {
		t.Fatalf("state = %q, want settled once a covering verification passed", op.State)
	}
	if gaps := a.readinessOperationGaps(); len(gaps) != 0 {
		t.Fatalf("gap survived a covering verification: %+v", gaps)
	}
}

func TestRetiredDeliveryStampProducesNoGapReport(t *testing.T) {
	a, _ := newEvidenceAgent(t, evidenceWriter{}, true)
	plan, rec := mutationPlanAndReceipt("internal/auth/login.go")
	a.recordOperationOutcome(plan, rec, nil)

	report := describeReadinessGaps(a.readinessOperationGaps())
	if report != "" {
		t.Fatalf("retired delivery stamp produced gap report %q for %s", report, rec.OperationID)
	}
}

func TestPausedOperationIsReportedForTheUserNotTheModel(t *testing.T) {
	writer := evidenceWriter{target: tool.EvidenceTargetInfo{
		Path: "/w/a.go", WholeFile: true, Hashes: hashesFor("alpha", "beta"),
	}}
	a, _ := newEvidenceAgent(t, writer, true)
	runEvidenceGate(a, "/w/a.go")
	runEvidenceGate(a, "/w/a.go")

	gaps := a.readinessOperationGaps()
	if len(gaps) != 1 || gaps[0].Action != readinessActionResolveWithUser {
		t.Fatalf("gaps = %+v, want one resolve_with_user entry", gaps)
	}
}
