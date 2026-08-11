package s3bench

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/LIHA-Research/lihacloud-bench/internal/config"
	"github.com/LIHA-Research/lihacloud-bench/internal/manifest"
	"github.com/google/uuid"
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

func TestIntegration(t *testing.T) {
	endpoint := os.Getenv("LIHACLOUD_BENCH_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("LIHACLOUD_BENCH_TEST_S3_ENDPOINT is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	engine, err := New(ctx, config.S3Config{
		Enabled:      true,
		Endpoint:     endpoint,
		Region:       "us-east-1",
		PathStyle:    true,
		BucketPrefix: "lihacloud-bench",
	})
	if err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	outcome := engine.Run(ctx, Spec{
		RunID:       runID,
		ToolVersion: "test",
		Profile:     "quick",
		LargeBytes:  mebibyte,
		LargeTrials: 1,
		SmallCount:  10,
		Comparable:  false,
	}, func(manifest.Resource) error { return nil })
	if outcome.Benchmark.Status != "success" || len(outcome.Benchmark.Metrics) != 24 || !outcome.Resource.Owned {
		t.Fatalf("unexpected result: %+v", outcome)
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
	defer cleanupCancel()
	if err := engine.Cleanup(cleanupCtx, runID, outcome.Manifest); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}
