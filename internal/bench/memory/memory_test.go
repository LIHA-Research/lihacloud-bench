package memory

import (
	"context"
	"testing"
)

func TestRun(t *testing.T) {
	result := Run(context.Background(), Spec{Size: 64 * 1024, Trials: 1, Workers: 2})
	if result.Status != "success" || len(result.Metrics) != 12 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestInvalidSpec(t *testing.T) {
	result := Run(context.Background(), Spec{})
	if result.Status != "failed" || result.ErrorCode != "invalid_workload" {
		t.Fatalf("unexpected result: %+v", result)
	}
}
