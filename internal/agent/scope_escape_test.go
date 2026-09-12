package agent

import (
	"encoding/json"
	"testing"

	"reasonix/internal/plancontract"
	"reasonix/internal/tool"
)

func scopedAgent(t *testing.T, plan *plancontract.Plan) *Agent {
	t.Helper()
	a := New(nil, tool.NewRegistry(), NewSession(""), Options{}, nil)
	a.SetPlanContract(plan)
	return a
}

func writeArgs(path string) json.RawMessage {
	return json.RawMessage(`{"path":"` + path + `","content":"x"}`)
}

func TestPlanPredictedPathsDoNotTriggerQualityApproval(t *testing.T) {
	plan := plancontract.Plan{Objective: "fix", Steps: []plancontract.Step{{Title: "fix", VerifiedFiles: []string{"internal/pay/retry.go"}}}}.Normalize()
	a := scopedAgent(t, &plan)
	proposal := a.recoveryProposal(&toolCallPlan{evidenceName: "write_file", evidenceArgs: writeArgs("internal/other/file.go")}, "", "write", "preview")
	if proposal.ExpandedScope {
		t.Fatal("predicted plan paths became an approval boundary")
	}
}
