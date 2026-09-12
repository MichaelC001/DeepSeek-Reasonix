package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/provider"
	"reasonix/internal/tool"

	_ "reasonix/internal/tool/builtin"
)

type stubBash struct{}

func (stubBash) Name() string        { return "bash" }
func (stubBash) Description() string { return "stub bash" }
func (stubBash) ReadOnly() bool      { return false }
func (stubBash) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`)
}
func (stubBash) Execute(context.Context, json.RawMessage) (string, error) { return "ok", nil }

type stubWrite struct{}

func (stubWrite) Name() string        { return "write_file" }
func (stubWrite) Description() string { return "stub write" }
func (stubWrite) ReadOnly() bool      { return false }
func (stubWrite) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
}
func (stubWrite) Execute(context.Context, json.RawMessage) (string, error) { return "wrote", nil }

// evidenceRegistry wires the real complete_step + todo_write builtins (the
// enforcement surface under test) alongside bash/write stubs that emit real
// receipts without touching the host — so the whole turn loop, ledger, gate,
// and host-advance run end to end.
func evidenceRegistry() *tool.Registry {
	reg := tool.NewRegistry()
	for _, bt := range tool.Builtins() {
		if bt.Name() == "complete_step" || bt.Name() == "todo_write" {
			reg.Add(bt)
		}
	}
	reg.Add(stubBash{})
	reg.Add(stubWrite{})
	return reg
}

func hostAdvances(sink *recordSink) int {
	n := 0
	for _, e := range sink.kinds(event.ToolResult) {
		if strings.HasPrefix(e.Tool.ID, "host-advance-") {
			n++
		}
	}
	return n
}

// sessionContains reports whether any message body holds sub — used to assert a
// tool's own result text (a complete_step evidence.ModelCompletionDeclarationPrefix or its rejection reason),
// since Run returns nil whether or not a tool call was rejected mid-turn.
func sessionContains(a *Agent, sub string) bool {
	for _, m := range a.Session().Messages {
		if strings.Contains(m.Content, sub) {
			return true
		}
	}
	return false
}

// Serial plan: the model establishes the list once, then signs off each step
// with complete_step — the host advances the list (no per-step todo_write, so
// the #3909 batch-completion failure can't arise) and a cited command tolerates
// a cd-prefix drift. The final answer is allowed once every step is signed off.
func TestE2ETodoWriteProgressThenOptionalCompleteStep(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "t0", Name: "todo_write",
			Arguments: `{"todos":[{"content":"test","status":"in_progress"},{"content":"vet","status":"pending"}]}`}}},
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "t1", Name: "todo_write",
			Arguments: `{"todos":[{"content":"test","status":"completed"},{"content":"vet","status":"in_progress"}]}`}}},
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "b1", Name: "bash",
			Arguments: `{"command":"go vet ./..."}`}}},
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "complete_step",
			Arguments: `{"step":"vet","result":"vet passes","evidence":[{"kind":"verification","summary":"vet passes","command":"go vet ./..."}]}`}}},
		testutil.Turn{Text: "all done"},
	)
	sink := &recordSink{}
	a := New(mp, evidenceRegistry(), NewSession("sys"), Options{}, sink)

	if err := a.Run(withNoClosedLoop(context.Background()), "implement the plan"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := a.CanonicalTodoState()
	if len(got) != 2 || got[0].Status != "completed" || got[1].Status != "completed" {
		t.Fatalf("canonical todos = %+v, want todo_write then complete_step to finish the list", got)
	}
	if n := hostAdvances(sink); n < 1 {
		t.Fatalf("host advanced %d times, want the complete_step path to still advance", n)
	}
}

// A command cited with a different string than it ran under (#2917: the model
// drops the cd-prefix) is still accepted via segment matching, in-turn.
func TestE2ECommandDriftAcceptedInTurn(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "b1", Name: "bash",
			Arguments: `{"command":"cd /Users/x/repo && git merge upstream/main --ff-only"}`}}},
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "complete_step",
			Arguments: `{"step":"sync","result":"synced","evidence":[{"kind":"verification","summary":"fast-forwarded","command":"git merge upstream/main --ff-only"}]}`}}},
		testutil.Turn{Text: "synced"},
	)
	a := New(mp, evidenceRegistry(), NewSession("sys"), Options{}, event.Discard)
	a.SeedTodoState([]evidence.TodoItem{{Content: "sync", Status: "pending"}})
	if err := a.Run(withNoClosedLoop(context.Background()), "sync the branch"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sessionContains(a, evidence.ModelCompletionDeclarationPrefix) {
		t.Fatal("cd-prefixed command drift rejected a real verification")
	}
}

// Cross-turn: a prior turn left an unfinished plan in the canonical state. A new
// turn that does work and prematurely claims "all done" without re-asserting the
// todos is blocked by the canonical fallback, then clears once both steps are
// actually signed off (host-advanced) — the loop that #2917 could not close.
func TestE2ECrossTurnDiffEvidenceViaSessionFallback(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "w1", Name: "write_file", Arguments: `{"path":"pkg/x.go"}`}}},
		testutil.Turn{Text: "edited x.go"},
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "complete_step",
			Arguments: `{"step":"edit x","result":"x updated","evidence":[{"kind":"diff","summary":"changed x","paths":["pkg/x.go"]}]}`}}},
		testutil.Turn{Text: evidence.ModelCompletionDeclarationPrefix},
	)
	a := New(mp, evidenceRegistry(), NewSession("sys"), Options{}, event.Discard)

	if err := a.Run(withNoClosedLoop(context.Background()), "edit x.go without tests"); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if err := a.Run(withNoClosedLoop(context.Background()), "now sign off that change"); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	if !sessionContains(a, evidence.ModelCompletionDeclarationPrefix) {
		t.Fatal("turn 2 rejected a cross-turn diff citation the session proves")
	}
}

// A diff citation for a file no turn ever wrote stays rejected — the session
// fallback widens what counts as proof, it does not wave through fabrication.
