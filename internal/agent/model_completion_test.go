package agent

import (
	"context"
	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/instruction"
	"reasonix/internal/provider"
	"reflect"
	"testing"
)

// The same permissioned tool loop handles every path and instruction. No path,
// task list, plan criterion or verification request adds a completion gate.
func TestModelEndsWithoutHostQualityObligations(t *testing.T) {
	for _, path := range []string{"README.md", "internal/auth/login.go", "migrations/001.sql", "internal/control/architecture.go"} {
		for _, goal := range []bool{false, true} {
			t.Run(path+map[bool]string{false: "/ordinary", true: "/goal"}[goal], func(t *testing.T) {
				prov := &scriptedProvider{name: "model", turns: [][]provider.Chunk{
					{toolCallChunk("w", "write_file", `{"path":"`+path+`"}`), {Type: provider.ChunkDone}},
					{{Type: provider.ChunkText, Text: "Changed the file. Tests were not run."}, {Type: provider.ChunkDone}},
				}}
				a := New(prov, evidenceRegistry(), NewSession("sys"), Options{ProjectChecks: []instruction.VerifyCheck{{Command: "go test ./...", SourcePath: "AGENTS.md"}}}, event.Discard)
				todo := []evidence.TodoItem{{Content: "pending check", Status: "in_progress"}}
				a.SeedTodoState(todo)
				ctx := context.Background()
				if goal {
					ctx = withClosedLoopContext(ctx)
				}
				if err := a.Run(ctx, "Make the change and perform full verification"); err != nil {
					t.Fatal(err)
				}
				if prov.call != 2 || !reflect.DeepEqual(a.CanonicalTodoState(), todo) {
					t.Fatalf("host resumed or changed todos: calls=%d todos=%+v", prov.call, a.CanonicalTodoState())
				}
				receipt := a.CompletionReceipt()
				if receipt == nil || receipt.AssessmentKind != "facts" || receipt.Verdict != "unknown" || len(receipt.Verifications) != 0 {
					t.Fatalf("invented quality result: %+v", receipt)
				}
				if a.PrepareFinalReadinessRecovery() {
					t.Fatal("quality gap created recovery")
				}
			})
		}
	}
}

func TestDeclarationReplayDoesNotAdvanceOtherTodos(t *testing.T) {
	sess := NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "t", Name: "todo_write", Arguments: `{"todos":[{"content":"a","status":"in_progress"},{"content":"b","status":"pending"}]}`}}})
	sess.Add(provider.Message{Role: provider.RoleTool, Name: "todo_write", ToolCallID: "t", Content: "Model task list updated: 2 total"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c", Name: "complete_step", Arguments: `{"step":"b","result":"done"}`}}})
	sess.Add(provider.Message{Role: provider.RoleTool, Name: "complete_step", ToolCallID: "c", Content: evidence.ModelCompletionDeclarationPrefix + "2"})
	before := sess.Snapshot()
	a := New(nil, evidenceRegistry(), sess, Options{}, event.Discard)
	a.RebuildTodoState()
	got := a.CanonicalTodoState()
	if len(got) != 2 || got[0].Status != "in_progress" || got[1].Status != "completed" {
		t.Fatalf("replayed inferred progress: %+v", got)
	}
	if !reflect.DeepEqual(before, sess.Snapshot()) {
		t.Fatal("projection rewrote provider history")
	}
}
