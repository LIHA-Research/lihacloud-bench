package cpu

import (
	"context"
	"testing"
	"time"
)

func TestRun(t *testing.T) {
	result := Run(context.Background(), Spec{Duration: 15 * time.Millisecond, Trials: 1, BlockSize: 4096, Workers: 2})
	if result.Status != "success" || len(result.Metrics) != 5 {
		t.Fatalf("unexpected result: %+v", result)
	}
	for _, metric := range result.Metrics {
		if metric.Value < 0 {
			t.Fatalf("negative metric: %+v", metric)
		}
	}
}

func TestCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := Run(ctx, Spec{Duration: time.Second, Trials: 1, BlockSize: 4096, Workers: 1})
	if result.Status != "failed" {
		t.Fatalf("status=%s", result.Status)
	}
}
