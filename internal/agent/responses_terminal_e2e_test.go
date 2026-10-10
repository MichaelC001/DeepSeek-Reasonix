package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/provider/responses"
)

// Each response travels through the real adapter and Agent; only HTTP and tools
// are local fixtures. Interrupted calls must never become committed work.
func TestResponsesTerminalToolIntegrity(t *testing.T) {
	const text = `{"type":"response.output_text.delta","item_id":"msg_1","content_index":0,"delta":"现在打开浏览器访问 ComfyUI："}`
	const start = `{"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"echo","arguments":""}}`
	const args = `{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"text\":\"hi\"}"}`
	const partial = `{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"text\":"}`
	const done = `{"type":"response.function_call_arguments.done","item_id":"fc_1","name":"echo","arguments":"{\"text\":\"hi\"}"}`
	const itemDone = `{"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"echo","status":"completed","arguments":"{\"text\":\"hi\"}"}}`
	const complete = `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"现在打开浏览器访问 ComfyUI：","annotations":[]}]},{"id":"fc_1","type":"function_call","call_id":"call_1","name":"echo","status":"completed","arguments":"{\"text\":\"hi\"}"}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`
	const noCall = `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"现在打开浏览器访问 ComfyUI：","annotations":[]}]}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`
	const incomplete = `{"type":"response.incomplete","response":{"id":"resp_1","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"现在打开浏览器访问 ComfyUI：","annotations":[]}]},{"id":"fc_1","type":"function_call","call_id":"call_1","name":"echo","status":"incomplete","arguments":"{\"text\":"}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`
	const malformed = `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"现在打开浏览器访问 ComfyUI：","annotations":[]}]},{"id":"fc_1","type":"function_call","call_id":"call_1","name":"echo","status":"completed","arguments":{"text":"hi"}}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`
	cases := []struct {
		name      string
		events    []string
		wantTools int
	}{
		{"schema_shaped_tool_lifecycle", []string{`{"type":"response.created","response":{"id":"resp_1","status":"in_progress","output":[]}}`, `{"type":"response.in_progress","response":{"id":"resp_1","status":"in_progress","output":[]}}`, `{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"in_progress","content":[]}}`, text, `{"type":"response.output_text.done","item_id":"msg_1","content_index":0,"text":"现在打开浏览器访问 ComfyUI："}`, `{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"现在打开浏览器访问 ComfyUI：","annotations":[]}]}}`, start, args, done, itemDone, complete}, 1},
		{"completed_output_missing_done_events", []string{text, start, args, complete}, 1},
		{"transition_text_without_calls", []string{text, noCall}, 0},
		{"partial_call_then_DONE", []string{text, start, partial, "[DONE]"}, 0},
		{"partial_call_then_completed", []string{text, start, partial, noCall}, 0},
		{"partial_call_then_incomplete_max_output", []string{text, start, partial, incomplete}, 0},
		{"partial_call_then_EOF", []string{text, start, partial}, 0},
		{"malformed_arguments_object_in_terminal", []string{text, start, args, malformed}, 0},
		{"malformed_terminal_without_start", []string{text, malformed}, 0},
		{"completed_call_before_malformed_terminal", []string{text, start, args, done, itemDone, strings.Replace(malformed, `"call_id":"call_1"`, `"call_id":"call_2"`, 1)}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := requests.Add(1)
				body, _ := io.ReadAll(r.Body)
				if n > 1 && tc.wantTools > 0 && (!bytes.Contains(body, []byte(`"type":"function_call_output"`)) || !bytes.Contains(body, []byte(`"call_id":"call_1"`))) {
					t.Error("continuation omitted matching tool output")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if n > 1 {
					_, _ = io.WriteString(w, responsesFinalAnswerSSE)
					return
				}
				for _, s := range tc.events {
					_, _ = fmt.Fprintf(w, "data: %s\n\n", s)
				}
			}))
			defer srv.Close()
			p := responses.New(responses.Config{Name: "mimo-diagnostic", APIKey: "fake-local-only", BaseURL: "https://api.xiaomimimo.com/v1", RequestURL: srv.URL, Model: "mimo-v2.6-flash", Effort: "high"})
			sink := &recordSink{}
			a := New(p, echoRegistry(), NewSession(""), Options{MissingReasoningWarnStateDir: t.TempDir()}, sink)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := a.Run(ctx, "请执行 echo 工具，再报告实际结果")
			tools := len(sink.kinds(event.ToolResult))
			var savedCalls int
			var finals []string
			for _, m := range a.Session().Snapshot() {
				if m.Role == provider.RoleAssistant {
					savedCalls += len(m.ToolCalls)
					if !m.LocalOnly {
						finals = append(finals, m.Content)
					}
				}
			}
			var notices []string
			for _, e := range sink.kinds(event.Notice) {
				notices = append(notices, e.Text)
			}
			t.Logf("requests=%d tool_results=%d saved_calls=%d retries=%d err=%v final=%q notices=%q", requests.Load(), tools, savedCalls, len(sink.kinds(event.Retrying)), err, strings.Join(finals, " | "), notices)
			wantRequests := int32(1)
			if tc.wantTools > 0 {
				wantRequests = 2
			}
			if requests.Load() != wantRequests {
				t.Errorf("requests=%d want=%d", requests.Load(), wantRequests)
			}
			wantInterrupted := tc.name == "partial_call_then_EOF" || tc.name == "partial_call_then_DONE" || tc.name == "partial_call_then_completed" || strings.Contains(tc.name, "malformed")
			if wantInterrupted {
				if err == nil {
					t.Error("want explicit error for unsafe terminal")
				}
				if tc.name != "partial_call_then_EOF" && provider.IsStreamInterrupted(err) {
					t.Error("complete protocol error must not become retryable stream interruption")
				}
				if tc.name == "partial_call_then_EOF" && !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Errorf("err=%v want unexpected EOF", err)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if len(sink.kinds(event.Retrying)) != 0 {
				t.Error("unexpected retry")
			}
			if savedCalls != tc.wantTools {
				t.Errorf("saved calls=%d want=%d", savedCalls, tc.wantTools)
			}
			if tc.wantTools > 0 {
				if len(finals) == 0 || finals[len(finals)-1] != "done" {
					t.Errorf("finals=%q want final done", finals)
				}
			} else if !wantInterrupted {
				if len(finals) != 1 || finals[0] != "现在打开浏览器访问 ComfyUI：" {
					t.Errorf("finals=%q", finals)
				}
			}
			if wantInterrupted && len(finals) != 0 {
				t.Errorf("interrupted attempt committed normal final %q", finals)
			}
			joinedNotices := strings.Join(notices, " ")
			if tc.name == "partial_call_then_incomplete_max_output" && !strings.Contains(joinedNotices, "response truncated") {
				t.Error("missing truncation notice")
			}
			if tc.name == "partial_call_then_EOF" && !strings.Contains(joinedNotices, "stream ended before completion") {
				t.Error("missing EOF notice")
			}
			if tc.wantTools == 0 && tc.name != "partial_call_then_incomplete_max_output" && !wantInterrupted && len(notices) != 0 {
				t.Errorf("unexpected notices %q", notices)
			}
			if tools != tc.wantTools {
				t.Errorf("tool results=%d want=%d", tools, tc.wantTools)
			}
		})
	}
}
