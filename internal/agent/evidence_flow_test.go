package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/instruction"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// scriptedProvider replays a distinct chunk set per Stream call, so a multi-turn
// Run() sees tool calls on turn 1 and a plain final answer on turn 2.
type scriptedProvider struct {
	name     string
	turns    [][]provider.Chunk
	call     int
	requests []provider.Request
}

func (s *scriptedProvider) Name() string { return s.name }

func (s *scriptedProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	s.requests = append(s.requests, req)
	i := s.call
	if i >= len(s.turns) {
		i = len(s.turns) - 1
	}
	s.call++
	ch := make(chan provider.Chunk, len(s.turns[i]))
	for _, c := range s.turns[i] {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func toolCallChunk(id, name, args string) provider.Chunk {
	return provider.Chunk{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: id, Name: name, Arguments: args}}
}

func toolResult(s *Session, name string) string {
	for _, m := range s.Messages {
		if m.Role == provider.RoleTool && m.Name == name {
			return m.Content
		}
	}
	return ""
}

func lastToolResult(s *Session, name string) string {
	var result string
	for _, m := range s.Messages {
		if m.Role == provider.RoleTool && m.Name == name {
			result = m.Content
		}
	}
	return result
}

func toolResultByID(s *Session, id string) string {
	for _, m := range s.Messages {
		if m.Role == provider.RoleTool && m.ToolCallID == id {
			return m.Content
		}
	}
	return ""
}

func toolResults(s *Session, name string) []string {
	var results []string
	for _, m := range s.Messages {
		if m.Role == provider.RoleTool && m.Name == name {
			results = append(results, m.Content)
		}
	}
	return results
}

func sessionHasUserMessageContaining(s *Session, needle string) bool {
	for _, m := range s.Messages {
		if m.Role != provider.RoleUser {
			continue
		}
		if strings.Contains(m.Content, needle) {
			return true
		}
		// Fallback: provider projection (RawContent stripped, Content kept).
		projected := provider.ModelMessages([]provider.Message{m})
		if len(projected) > 0 && strings.Contains(projected[0].Content, needle) {
			return true
		}
	}
	return false
}

type readinessAuditSink struct {
	events []evidence.ReadinessAudit
}

func (s *readinessAuditSink) Emit(event.Event) {}

func (s *readinessAuditSink) RecordReadinessAudit(a evidence.ReadinessAudit) {
	s.events = append(s.events, a)
}

// TestEvidenceFlowEndToEnd drives a full Run(): turn 1 runs bash then signs the
// step off citing that exact command; complete_step must see the host receipt
// recorded earlier in the same batch and report it host-verified.
func TestEvidenceFlowEndToEnd(t *testing.T) {
	completeStep, ok := tool.LookupBuiltin("complete_step")
	if !ok {
		t.Fatal("complete_step builtin not registered")
	}
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "bash", readOnly: false})
	reg.Add(completeStep)

	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("c1", "bash", `{"command":"go test ./..."}`),
			toolCallChunk("c2", "complete_step", `{
				"step":"Run the suite",
				"result":"tests pass",
				"evidence":[{"kind":"verification","summary":"go test ./... passed","command":"go test ./..."}]
			}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}

	a := New(prov, reg, NewSession(""), Options{}, event.Discard)
	a.SeedTodoState([]evidence.TodoItem{{Content: "Run the suite", Status: "pending"}})
	if err := a.Run(withNoClosedLoop(context.Background()), "run the suite and sign the step off"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := toolResult(a.sess.conversation, "complete_step"); !strings.Contains(got, evidence.ModelCompletionDeclarationPrefix) {
		t.Fatalf("complete_step result = %q, want it host-verified from the bash receipt", got)
	}
}

func TestClosedLoopEnforcesAcceptanceReviewVerificationAndSignoff(t *testing.T) {
	reg := evidenceRegistry()
	reg.Add(fakeTool{name: "read_file", readOnly: true})
	// Keep review available so this ordinary production change exercises the
	// Medium-risk host-proof alternative instead of the minimal-registry bypass.
	reg.Add(fakeTool{name: "review", readOnly: true})

	prov := &scriptedProvider{name: "delivery", turns: [][]provider.Chunk{
		{toolCallChunk("blocked-write", "write_file", `{"path":"main.go","content":"package main"}`), {Type: provider.ChunkDone}},
		{toolCallChunk("criteria", "todo_write", `{"todos":[{"content":"Ship main","status":"in_progress","activeForm":"Shipping main"}]}`), {Type: provider.ChunkDone}},
		{toolCallChunk("write", "write_file", `{"path":"main.go","content":"package main"}`), {Type: provider.ChunkDone}},
		{toolCallChunk("review", "read_file", `{"path":"main.go"}`), {Type: provider.ChunkDone}},
		{toolCallChunk("verify", "bash", `{"command":"go test ./..."}`), {Type: provider.ChunkDone}},
		{toolCallChunk("signoff", "complete_step", `{
			"step":"Ship main",
			"result":"main is implemented and verified",
			"evidence":[
				{"kind":"diff","summary":"main implementation added","paths":["main.go"]},
				{"kind":"verification","summary":"tests pass","command":"go test ./..."}
			]
		}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "delivered"}, {Type: provider.ChunkDone}},
	}}

	a := New(prov, reg, NewSession(""), Options{}, event.Discard)
	if err := a.Run(withClosedLoopContext(context.Background()), "implement main"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := toolResultByID(a.sess.conversation, "blocked-write"); strings.Contains(got, "acceptance criteria") {
		t.Fatalf("single-file write must not require a todo precondition: %q", got)
	}
	if sessionHasUserMessageContaining(a.sess.conversation, "<execution-policy") {
		t.Fatal("new turns must not inject execution-policy")
	}
	if got := lastToolResult(a.sess.conversation, "complete_step"); !strings.Contains(got, evidence.ModelCompletionDeclarationPrefix) {
		t.Fatalf("complete_step result = %q, want successful sign-off", got)
	}
	firstSystem := systemMessageContent(prov.requests[0])
	firstTools := prov.requests[0].Tools
	for i, req := range prov.requests[1:] {
		if got := systemMessageContent(req); got != firstSystem {
			t.Fatalf("delivery request %d changed the cache-stable system prompt", i+2)
		}
		if !reflect.DeepEqual(req.Tools, firstTools) {
			t.Fatalf("delivery request %d changed provider-visible tool schemas", i+2)
		}
	}
}

func systemMessageContent(req provider.Request) string {
	for _, msg := range req.Messages {
		if msg.Role == provider.RoleSystem {
			return msg.Content
		}
	}
	return ""
}

func TestClosedLoopCommandOnlyActionRequiresCriteriaAndSignoff(t *testing.T) {
	reg := evidenceRegistry()
	prov := &scriptedProvider{name: "delivery", turns: [][]provider.Chunk{
		{toolCallChunk("blocked-test", "bash", `{"command":"go test ./..."}`), {Type: provider.ChunkDone}},
		{toolCallChunk("criteria", "todo_write", `{"todos":[{"content":"Run tests","status":"in_progress"}]}`), {Type: provider.ChunkDone}},
		{toolCallChunk("verify", "bash", `{"command":"go test ./..."}`), {Type: provider.ChunkDone}},
		{toolCallChunk("signoff", "complete_step", `{"step":"Run tests","result":"tests pass","evidence":[{"kind":"verification","summary":"tests pass","command":"go test ./..."}]}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "tests pass"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)
	if err := a.Run(withClosedLoopContext(context.Background()), "run tests"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := toolResultByID(a.sess.conversation, "blocked-test"); strings.Contains(got, "acceptance criteria") {
		t.Fatalf("verification command must not require a todo precondition: %q", got)
	}
	if got := lastToolResult(a.sess.conversation, "complete_step"); !strings.Contains(got, evidence.ModelCompletionDeclarationPrefix) {
		t.Fatalf("complete_step result = %q, want successful command-only sign-off", got)
	}
}

func TestClosedLoopExplainsMaskedVerifierExitBeforeExecution(t *testing.T) {
	reg := evidenceRegistry()
	prov := &scriptedProvider{name: "delivery", turns: [][]provider.Chunk{
		{toolCallChunk("criteria", "todo_write", `{"todos":[{"content":"Check snake","status":"in_progress"}]}`), {Type: provider.ChunkDone}},
		{toolCallChunk("masked", "bash", `{"command":"tail -n +2 snake.js | head -n 20 | node --check -; echo \"EXIT: $?\""}`), {Type: provider.ChunkDone}},
		{toolCallChunk("safe", "bash", `{"command":"tail -n +2 snake.js | head -n 20 | node --check -"}`), {Type: provider.ChunkDone}},
		{toolCallChunk("signoff", "complete_step", `{"step":"Check snake","result":"syntax valid","evidence":[{"kind":"verification","summary":"syntax valid","command":"tail -n +2 snake.js | head -n 20 | node --check -"}]}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "checked"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)
	if err := a.Run(withClosedLoopContext(context.Background()), "check the snake game"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := toolResultByID(a.sess.conversation, "masked"); !strings.Contains(got, "masks the verifier's exit status") {
		t.Fatalf("masked command result = %q, want precise exit-status guidance", got)
	}
	if _, ok := a.task.ledger.LatestSuccessfulMutationIndex(); ok {
		t.Fatal("blocked masked verifier must not become a successful mutation")
	}
}

func TestClosedLoopAllowsEvidenceBackedReadOnlyAnalysis(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "read_file", readOnly: true})
	prov := &scriptedProvider{name: "delivery", turns: [][]provider.Chunk{
		{toolCallChunk("read", "read_file", `{"path":"main.go"}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "analysis"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)
	if err := a.Run(withClosedLoopContext(context.Background()), "analyze main.go"); err != nil {
		t.Fatalf("read-only analysis should not require mutation/sign-off: %v", err)
	}
}

func TestEvidenceFlowEnforcesProjectChecksAfterWrite(t *testing.T) {
	completeStep, ok := tool.LookupBuiltin("complete_step")
	if !ok {
		t.Fatal("complete_step builtin not registered")
	}
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "write_file", readOnly: false})
	reg.Add(fakeTool{name: "bash", readOnly: false})
	reg.Add(completeStep)

	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("c1", "write_file", `{"path":"changed.go","content":"package main"}`),
			toolCallChunk("c2", "bash", `{"command":"go test ./..."}`),
			toolCallChunk("c3", "complete_step", `{
				"step":"Edit code",
				"result":"changed.go updated",
				"evidence":[{"kind":"diff","summary":"updated code","paths":["changed.go"]}]
			}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}

	a := New(prov, reg, NewSession(""), Options{
		ProjectChecks: []instruction.VerifyCheck{{Command: "go test ./...", SourcePath: "AGENTS.md", Line: 3}},
	}, event.Discard)
	a.SeedTodoState([]evidence.TodoItem{{Content: "Edit code", Status: "pending"}})
	if err := a.Run(withNoClosedLoop(context.Background()), "edit and verify"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := toolResult(a.sess.conversation, "complete_step")
	if !strings.Contains(got, evidence.ModelCompletionDeclarationPrefix) {
		t.Fatalf("complete_step result = %q, want project check verified from same batch", got)
	}
}

func TestFinalReadinessAllowsFinalAnswerWithoutWriter(t *testing.T) {
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, tool.NewRegistry(), NewSession(""), Options{
		ProjectChecks: []instruction.VerifyCheck{{Command: "go test ./...", SourcePath: "AGENTS.md", Line: 3}},
	}, event.Discard)

	if err := a.Run(withNoClosedLoop(context.Background()), "inspect only"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if prov.call != 1 {
		t.Fatalf("provider calls = %d, want 1", prov.call)
	}
}

func TestFinalReadinessAllowsWriterWithoutChecksOrTodos(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "write_file", readOnly: false})
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("c1", "write_file", `{"path":"changed.go","content":"package main"}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)

	if err := a.Run(withNoClosedLoop(context.Background()), "simple edit"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if prov.call != 2 {
		t.Fatalf("provider calls = %d, want 2", prov.call)
	}
}

func TestFinalReadinessAuditSkipsWhenGateDoesNotApply(t *testing.T) {
	t.Run("no writer", func(t *testing.T) {
		prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
			{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
		}}
		sink := &readinessAuditSink{}
		a := New(prov, tool.NewRegistry(), NewSession(""), Options{
			ProjectChecks: []instruction.VerifyCheck{{Command: "go test ./...", SourcePath: "AGENTS.md", Line: 3}},
		}, sink)

		if err := a.Run(withNoClosedLoop(context.Background()), "inspect only"); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(sink.events) != 0 {
			t.Fatalf("readiness audit events = %d, want 0: %+v", len(sink.events), sink.events)
		}
	})

	t.Run("writer without checks or todo", func(t *testing.T) {
		reg := tool.NewRegistry()
		reg.Add(fakeTool{name: "write_file", readOnly: false})
		prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
			{
				toolCallChunk("c1", "write_file", `{"path":"changed.go","content":"package main"}`),
				{Type: provider.ChunkDone},
			},
			{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
		}}
		sink := &readinessAuditSink{}
		a := New(prov, reg, NewSession(""), Options{}, sink)

		if err := a.Run(withNoClosedLoop(context.Background()), "simple edit"); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(sink.events) != 0 {
			t.Fatalf("readiness audit events = %d, want 0: %+v", len(sink.events), sink.events)
		}
	})
}

func TestFinalReadinessPermissionLoopGuardAllowsBlockedFinal(t *testing.T) {
	todoWrite, ok := tool.LookupBuiltin("todo_write")
	if !ok {
		t.Fatal("todo_write builtin not registered")
	}
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "write_file", readOnly: false})
	reg.Add(fakeTool{name: "bash", readOnly: false})
	reg.Add(todoWrite)
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("w1", "write_file", `{"path":"changed.go","content":"package main"}`),
			toolCallChunk("t1", "todo_write", `{"todos":[{"content":"Edit code","status":"in_progress"}]}`),
			{Type: provider.ChunkDone},
		},
		{toolCallChunk("b1", "bash", `{"command":"go test ./..."}`), {Type: provider.ChunkDone}},
		{toolCallChunk("b2", "bash", `{"command":"git status --short"}`), {Type: provider.ChunkDone}},
		{toolCallChunk("b3", "bash", `{"command":"ls -la"}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "blocked by permission"}, {Type: provider.ChunkDone}},
	}}
	sink, notices := noticeRecorder()
	a := New(prov, reg, NewSession(""), Options{
		Gate: &stubGate{deny: map[string]bool{"bash": true}},
	}, sink)

	if err := a.Run(withNoClosedLoop(context.Background()), "edit with todo, then hit bash permission blocks"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if prov.call != 5 {
		t.Fatalf("provider calls = %d, want writer turn, three blocked bash calls, then final", prov.call)
	}
	if got := lastToolResult(a.sess.conversation, "bash"); !strings.Contains(got, "[loop guard]") {
		t.Fatalf("last bash result = %q, want permission loop guard", got)
	}
	if got := toolResults(a.sess.conversation, "bash"); len(got) != stormBreakThreshold {
		t.Fatalf("bash results = %d, want exactly %d blocked attempts", len(got), stormBreakThreshold)
	}
	if len(*notices) == 0 {
		t.Fatal("loop guard should emit a user-facing notice")
	}
}

// TestFinalReadinessPermissionLoopGuardAllowsBlockedFinalForBatch pins the
// multi-call variant: the guard text lands on the batch's FIRST result, so any
// detection keyed to the latest tool message misses it. The loop-guard pass is
// host state and must let the model report the blocker regardless of where in
// the batch the guard text sits.
func TestFinalReadinessPermissionLoopGuardAllowsBlockedFinalForBatch(t *testing.T) {
	todoWrite, ok := tool.LookupBuiltin("todo_write")
	if !ok {
		t.Fatal("todo_write builtin not registered")
	}
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "write_file", readOnly: false})
	reg.Add(fakeTool{name: "bash", readOnly: false})
	reg.Add(todoWrite)
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("w1", "write_file", `{"path":"changed.go","content":"package main"}`),
			toolCallChunk("t1", "todo_write", `{"todos":[{"content":"Edit code","status":"in_progress"}]}`),
			{Type: provider.ChunkDone},
		},
		{
			toolCallChunk("b1a", "bash", `{"command":"go test ./..."}`),
			toolCallChunk("b1b", "bash", `{"command":"go vet ./..."}`),
			{Type: provider.ChunkDone},
		},
		{
			toolCallChunk("b2a", "bash", `{"command":"git status --short"}`),
			toolCallChunk("b2b", "bash", `{"command":"git diff --stat"}`),
			{Type: provider.ChunkDone},
		},
		{
			toolCallChunk("b3a", "bash", `{"command":"ls -la"}`),
			toolCallChunk("b3b", "bash", `{"command":"pwd"}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "blocked by permission"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{
		Gate: &stubGate{deny: map[string]bool{"bash": true}},
	}, event.Discard)

	if err := a.Run(withNoClosedLoop(context.Background()), "edit with todo, then hit batched bash permission blocks"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if prov.call != 5 {
		t.Fatalf("provider calls = %d, want writer turn, three blocked batches, then final", prov.call)
	}
	results := toolResults(a.sess.conversation, "bash")
	if len(results) != 2*stormBreakThreshold {
		t.Fatalf("bash results = %d, want %d blocked attempts across three batches", len(results), 2*stormBreakThreshold)
	}
	if !strings.Contains(results[len(results)-2], "[loop guard]") {
		t.Fatalf("first result of the guarded batch should carry the loop guard, got: %q", results[len(results)-2])
	}
	if strings.Contains(results[len(results)-1], "[loop guard]") {
		t.Fatalf("last result of the guarded batch should stay untouched (the pass must not depend on it), got: %q", results[len(results)-1])
	}
}

func TestTodoWriteOnlyTurnMayEndWithIncompleteTodos(t *testing.T) {
	todoWrite, ok := tool.LookupBuiltin("todo_write")
	if !ok {
		t.Fatal("todo_write builtin not registered")
	}
	reg := tool.NewRegistry()
	reg.Add(todoWrite)
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("c1", "todo_write", `{"todos":[{"content":"Draft plan","status":"in_progress"},{"content":"Implement","status":"pending"}]}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "here is the task list"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)

	if err := a.Run(withNoClosedLoop(context.Background()), "create a todo list only"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if prov.call != 2 {
		t.Fatalf("provider calls = %d, want 2 without readiness retry", prov.call)
	}
	if got := lastToolResult(a.sess.conversation, "todo_write"); !strings.Contains(got, "Model task list updated") {
		t.Fatalf("todo_write result = %q, want successful todo update", got)
	}
}

func TestReadOnlyContextAndTodoTurnMayEndWithIncompleteTodos(t *testing.T) {
	todoWrite, ok := tool.LookupBuiltin("todo_write")
	if !ok {
		t.Fatal("todo_write builtin not registered")
	}
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "read_file", readOnly: true})
	reg.Add(todoWrite)
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("c1", "read_file", `{"path":"README.md"}`),
			toolCallChunk("c2", "todo_write", `{"todos":[{"content":"Draft plan","status":"in_progress"},{"content":"Implement","status":"pending"}]}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "I reviewed the context and wrote the list."}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)

	if err := a.Run(withNoClosedLoop(context.Background()), "read context and only draft a todo list"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if prov.call != 2 {
		t.Fatalf("provider calls = %d, want 2 without readiness retry", prov.call)
	}
	if got := lastToolResult(a.sess.conversation, "todo_write"); !strings.Contains(got, "Model task list updated") {
		t.Fatalf("todo_write result = %q, want successful todo update", got)
	}
}

func TestEvidenceFlowRejectsStepMissingFromTodoWrite(t *testing.T) {
	todoWrite, ok := tool.LookupBuiltin("todo_write")
	if !ok {
		t.Fatal("todo_write builtin not registered")
	}
	completeStep, ok := tool.LookupBuiltin("complete_step")
	if !ok {
		t.Fatal("complete_step builtin not registered")
	}
	reg := tool.NewRegistry()
	reg.Add(todoWrite)
	reg.Add(completeStep)

	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("c1", "todo_write", `{"todos":[{"content":"Add parser","status":"in_progress"}]}`),
			toolCallChunk("c2", "complete_step", `{
				"step":"Ship parser",
				"result":"step is complete",
				"evidence":[{"kind":"manual","summary":"checked manually"}]
			}`),
			toolCallChunk("c3", "complete_step", `{
				"step":"Add parser",
				"result":"parser added",
				"evidence":[{"kind":"manual","summary":"checked manually"}]
			}`),
			toolCallChunk("c4", "todo_write", `{"todos":[{"content":"Add parser","status":"completed"}]}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}

	a := New(prov, reg, NewSession(""), Options{}, event.Discard)
	if err := a.Run(withNoClosedLoop(context.Background()), "update todos then sign off the wrong step"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := toolResult(a.sess.conversation, "complete_step")
	if !strings.Contains(got, "does not uniquely match") {
		t.Fatalf("complete_step result = %q, want todo-backed rejection", got)
	}
}

func TestEvidenceFlowAcceptsTodoCompletionAfterCompleteStep(t *testing.T) {
	todoWrite, ok := tool.LookupBuiltin("todo_write")
	if !ok {
		t.Fatal("todo_write builtin not registered")
	}
	completeStep, ok := tool.LookupBuiltin("complete_step")
	if !ok {
		t.Fatal("complete_step builtin not registered")
	}
	reg := tool.NewRegistry()
	reg.Add(todoWrite)
	reg.Add(completeStep)

	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("c1", "todo_write", `{"todos":[{"content":"Add parser","status":"in_progress"}]}`),
			toolCallChunk("c2", "complete_step", `{
				"step":"Add parser",
				"result":"parser added",
				"evidence":[{"kind":"manual","summary":"checked manually"}]
			}`),
			toolCallChunk("c3", "todo_write", `{"todos":[{"content":"Add parser","status":"completed"}]}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}

	a := New(prov, reg, NewSession(""), Options{}, event.Discard)
	if err := a.Run(withNoClosedLoop(context.Background()), "complete the todo with a sign-off first"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := lastToolResult(a.sess.conversation, "todo_write"); !strings.Contains(got, "Model task list updated") {
		t.Fatalf("final todo_write result = %q, want update accepted", got)
	}
}

func TestEvidenceFlowAcceptsTodoCompletionWithoutCompleteStep(t *testing.T) {
	todoWrite, ok := tool.LookupBuiltin("todo_write")
	if !ok {
		t.Fatal("todo_write builtin not registered")
	}
	completeStep, ok := tool.LookupBuiltin("complete_step")
	if !ok {
		t.Fatal("complete_step builtin not registered")
	}
	reg := tool.NewRegistry()
	reg.Add(todoWrite)
	reg.Add(completeStep)

	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("c1", "todo_write", `{"todos":[{"content":"Add parser","status":"in_progress"}]}`),
			toolCallChunk("c2", "todo_write", `{"todos":[{"content":"Add parser","status":"completed"}]}`),
			toolCallChunk("c3", "complete_step", `{
				"step":"Add parser",
				"result":"parser added",
				"evidence":[{"kind":"manual","summary":"checked manually"}]
			}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}

	a := New(prov, reg, NewSession(""), Options{}, event.Discard)
	if err := a.Run(withNoClosedLoop(context.Background()), "complete the todo without a sign-off"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	results := toolResults(a.sess.conversation, "todo_write")
	if len(results) < 2 || !strings.Contains(results[1], "1 completed") {
		t.Fatalf("todo_write results = %v, want progress accepted without complete_step", results)
	}
	got := a.CanonicalTodoState()
	if len(got) != 1 || got[0].Status != "completed" {
		t.Fatalf("canonical todos = %+v, want the item completed by todo_write", got)
	}
	if step := lastToolResult(a.sess.conversation, "complete_step"); !strings.Contains(step, evidence.ModelCompletionDeclarationPrefix) {
		t.Fatalf("complete_step result = %q, want a later optional receipt", step)
	}
}

func TestEvidenceFlowAllowsBatchCompleteStepSignoffs(t *testing.T) {
	todoWrite, ok := tool.LookupBuiltin("todo_write")
	if !ok {
		t.Fatal("todo_write builtin not registered")
	}
	completeStep, ok := tool.LookupBuiltin("complete_step")
	if !ok {
		t.Fatal("complete_step builtin not registered")
	}
	reg := tool.NewRegistry()
	reg.Add(todoWrite)
	reg.Add(completeStep)

	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("c1", "todo_write", `{"todos":[
				{"content":"Port entity imports","status":"in_progress"},
				{"content":"Run build and tests","status":"pending"}
			]}`),
			toolCallChunk("c2", "todo_write", `{"todos":[
				{"content":"Port entity imports","status":"completed"},
				{"content":"Run build and tests","status":"completed"}
			]}`),
			{Type: provider.ChunkDone},
		},
		{
			toolCallChunk("c3", "complete_step", `{
				"step":"Port entity imports",
				"result":"entity imports ported",
				"evidence":[{"kind":"manual","summary":"checked manually"}]
			}`),
			toolCallChunk("c4", "complete_step", `{
				"step":"Run build and tests",
				"result":"build and tests ran",
				"evidence":[{"kind":"manual","summary":"checked manually"}]
			}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}

	a := New(prov, reg, NewSession(""), Options{}, event.Discard)
	if err := a.Run(withNoClosedLoop(context.Background()), "recover from a rejected batch todo update"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	stepResults := toolResults(a.sess.conversation, "complete_step")
	if len(stepResults) != 2 {
		t.Fatalf("complete_step results = %v, want both sign-offs in one batch", stepResults)
	}
	for i, got := range stepResults {
		if !strings.Contains(got, evidence.ModelCompletionDeclarationPrefix) {
			t.Fatalf("batched complete_step result %d = %q, want successful sign-off", i+1, got)
		}
	}
	if got := prov.call; got != 3 {
		t.Fatalf("provider calls = %d, want todo setup, one sign-off batch, and final answer", got)
	}
	for i, todo := range a.CanonicalTodoState() {
		if todo.Status != "completed" {
			t.Fatalf("canonical todo %d = %+v, want completed", i+1, todo)
		}
	}
}

func TestEvidenceFlowTodoCompletionSurvivesFailedCompleteStep(t *testing.T) {
	todoWrite, ok := tool.LookupBuiltin("todo_write")
	if !ok {
		t.Fatal("todo_write builtin not registered")
	}
	completeStep, ok := tool.LookupBuiltin("complete_step")
	if !ok {
		t.Fatal("complete_step builtin not registered")
	}
	reg := tool.NewRegistry()
	reg.Add(todoWrite)
	reg.Add(completeStep)

	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("c1", "todo_write", `{"todos":[{"content":"Add parser","status":"in_progress"}]}`),
			toolCallChunk("c2", "complete_step", `{
				"step":"Ship parser",
				"result":"parser shipped",
				"evidence":[{"kind":"manual","summary":"checked manually"}]
			}`),
			toolCallChunk("c3", "todo_write", `{"todos":[{"content":"Add parser","status":"completed"}]}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}

	a := New(prov, reg, NewSession(""), Options{}, event.Discard)
	if err := a.Run(withClosedLoop(context.Background()), "attempt completion after a failed sign-off"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if step := lastToolResult(a.sess.conversation, "complete_step"); strings.Contains(step, evidence.ModelCompletionDeclarationPrefix) {
		t.Fatalf("complete_step result = %q, want the mismatched sign-off to fail", step)
	}
	results := toolResults(a.sess.conversation, "todo_write")
	if len(results) < 2 || !strings.Contains(results[1], "1 completed") {
		t.Fatalf("todo_write results = %v, want progress after a failed sign-off", results)
	}
	got := a.CanonicalTodoState()
	if len(got) != 1 || got[0].Status != "completed" {
		t.Fatalf("canonical todos = %+v, want completed despite failed complete_step", got)
	}
}
