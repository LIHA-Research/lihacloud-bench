package s3bench

import (
	"context"
	"testing"

	"github.com/LIHA-Research/lihacloud-bench/internal/config"
	"github.com/LIHA-Research/lihacloud-bench/internal/manifest"
)

func TestValidateBucket(t *testing.T) {
	runID := "77aa4a1a-4f8f-42a9-816c-982e1cba80db"
	if err := validateBucket("lihacloud-bench-"+runID, "lihacloud-bench", runID); err != nil {
		t.Fatal(err)
	}
	if err := validateBucket("customer-data", "lihacloud-bench", runID); err == nil {
		t.Fatal("unowned bucket was accepted")
	}
}

func TestCleanupRefusesUnrecordedResource(t *testing.T) {
	engine := NewWithClient(nil, config.S3Config{})
	err := engine.Cleanup(context.Background(), "77aa4a1a-4f8f-42a9-816c-982e1cba80db", manifest.Resource{Kind: "s3_bucket", Name: "customer-data"})
	if err == nil {
		t.Fatal("cleanup accepted a resource without matching local ownership evidence")
	}
}

func TestOperationMetrics(t *testing.T) {
	metrics := operationMetrics("put", operationResult{count: 2, latencies: []float64{1, 2}})
	if len(metrics) != 5 || metrics[1].Value != 1.5 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
}
