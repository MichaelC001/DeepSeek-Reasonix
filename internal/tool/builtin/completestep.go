package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"reasonix/internal/evidence"
	"reasonix/internal/tool"
)

func init() { tool.RegisterBuiltin(completeStep{}) }

// completeStep records a legacy model declaration without certifying it.
type completeStep struct{}

type stepEvidence struct {
	Kind        string   `json:"kind"`
	Summary     string   `json:"summary"`
	Command     string   `json:"command,omitempty"`
	Paths       []string `json:"paths,omitempty"`
	CriterionID string   `json:"criterion_id,omitempty"`
}

// validEvidenceKinds are the evidence forms a completion may cite. "checkpoint"
// (main's fourth kind) is omitted — v2 has no checkpoint system.
var validEvidenceKinds = map[string]bool{
	"verification": true, // a command/test was run; cite it and its outcome
	"review":       true, // a completed built-in review run, fresh for any later mutation
	"diff":         true, // a concrete code change; cite what changed
	"files":        true, // files created/edited/inspected; cite the paths
	"manual":       true, // a manual check; cite what was confirmed and how
}

func (completeStep) Name() string { return "complete_step" }

func (completeStep) Description() string {
	return "Deprecated compatibility entry: record a model completion declaration for one existing todo. This declaration does not certify verification or advance another item. Use todo_write for progress updates."
}

func (completeStep) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "step_id":{"type":"string","description":"PREFERRED: the stable step_id of the task-list item this completes, e.g. \"plan_step_02\". Unlike a title or a number it survives retitles, insertions, and reordering, so cite it whenever the item has one."},
  "step":{"type":"string","description":"Which plan step this completes — its title or number, matching the task list. Use only when the item has no step_id."},
  "step_index":{"type":"integer","minimum":1,"description":"Optional 1-based task-list item number. Use only when the item has no step_id; an index goes stale the moment a step is inserted above it."},
  "result":{"type":"string","description":"What is now true or changed as a result of finishing this step."},
  "evidence":{
    "type":"array",
    "description":"Optional model-supplied supporting statements, preserved without host certification.",
    "items":{
      "type":"object",
      "properties":{
        "criterion_id":{"type":"string","description":"Optional criterion referenced by the model declaration."},
        "kind":{"type":"string","enum":["verification","review","diff","files","manual"],"description":"The kind of supporting statement."},
        "summary":{"type":"string","description":"The evidence itself: the test result, what the diff does, or what was confirmed."},
        "command":{"type":"string","description":"Optional command cited by the model; actual execution records remain separate."},
        "paths":{"type":"array","items":{"type":"string"},"description":"Optional paths cited by the model."}
      },
      "required":["kind","summary"]
    }
  },
  "receipt_ids":{"type":"array","items":{"type":"string"},"description":"Optional historical receipt references; these do not certify the declaration."},
  "operation_id":{"type":"string","description":"Optional historical operation reference."},
  "notes":{"type":"string","description":"Optional caveats, follow-ups, or anything deferred."}
},
"required":["result"]
}`)
}

// ReadOnly is true: complete_step only records a claim (no filesystem or process
// effect), so it never needs approval and stays available alongside todo_write.
func (completeStep) ReadOnly() bool { return true }

// complete_step signs off execution work and is unavailable during planning.
// The host Plan gate remains authoritative for stale or hallucinated calls.
func (completeStep) ProviderVisible(ctx context.Context) bool {
	return false
}

// PlanModeSafe reports false: although complete_step is read-only, it signs off a
// completed execution step, which is meaningful only after plan approval — not
// during planning. This explicit phase opt-out is the Plan gate's enforced
// exception to the ordinary Permissions/Sandbox path.
func (completeStep) PlanModeSafe() bool { return false }

func (completeStep) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		StepID      string         `json:"step_id"`
		Step        string         `json:"step"`
		StepIndex   int            `json:"step_index"`
		Result      string         `json:"result"`
		Evidence    []stepEvidence `json:"evidence"`
		ReceiptIDs  []string       `json:"receipt_ids"`
		OperationID string         `json:"operation_id"`
		Notes       string         `json:"notes"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	step := completeStepIdentity(p.StepID, p.Step, p.StepIndex)
	if step == "" || p.StepIndex < 0 {
		return "", fmt.Errorf("a valid step_id, step, or positive step_index is required")
	}
	if strings.TrimSpace(p.Result) == "" {
		return "", fmt.Errorf("result is required")
	}
	for i, item := range p.Evidence {
		if !validEvidenceKinds[item.Kind] || strings.TrimSpace(item.Summary) == "" {
			return "", fmt.Errorf("evidence %d requires a known kind and a summary", i+1)
		}
	}
	match, _, err := verifyTodoStep(ctx, step)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(evidence.ModelCompletionDeclarationPrefix+"%d (%q): %s", match.Index, match.Content, strings.TrimSpace(p.Result)), nil
}

// verifyTodoStep resolves identity only; todo order and evidence are not gates.
func verifyTodoStep(ctx context.Context, step string) (evidence.TodoStepMatch, bool, error) {
	var todos []evidence.TodoItem
	if ledger, ok := evidence.FromContext(ctx); ok {
		todos, _ = ledger.LatestTodos()
	}
	if len(todos) == 0 {
		todos, _ = evidence.TodoStateFromContext(ctx)
	}
	match, ok := evidence.MatchStep(step, todos)
	if !ok {
		return match, false, fmt.Errorf("step %q does not uniquely match an existing todo; available ids: %s", step, strings.Join(evidence.TodoStepIDs(todos), ", "))
	}
	return match, true, nil
}

func completeStepIdentity(stepID, step string, stepIndex int) string {
	if id := strings.TrimSpace(stepID); id != "" {
		return id
	}
	if stepIndex > 0 {
		return strconv.Itoa(stepIndex)
	}
	return strings.TrimSpace(step)
}
