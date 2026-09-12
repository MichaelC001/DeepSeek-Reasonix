package builtin

import (
	"encoding/json"
	"reasonix/internal/evidence"
	"reflect"
	"testing"
)

func TestCompleteStepDeclarationPreservesActualReceipts(t *testing.T) {
	for _, success := range []bool{false, true} {
		ledger := evidence.NewLedger()
		ledger.Record(evidence.Receipt{ToolName: "bash", Command: "go test ./...", Success: success})
		before := ledger.Receipts()
		ctx := evidence.WithLedger(declaredStepContext(evidence.TodoItem{Content: "implement", Status: "in_progress"}), ledger)
		_, err := (completeStep{}).Execute(ctx, json.RawMessage(`{"step":"implement","result":"done","receipt_ids":["old-or-unknown"],"operation_id":"other-operation","evidence":[{"kind":"verification","summary":"model claim","command":"go test ./..."}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, ledger.Receipts()) {
			t.Fatal("declaration rewrote actual receipts")
		}
	}
}
