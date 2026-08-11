package clickhouse

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LIHA-Research/lihacloud-bench/internal/manifest"
	"github.com/google/uuid"
)

func TestPinnedQueries(t *testing.T) {
	queries, err := clickBenchQueries()
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 43 {
		t.Fatalf("pinned query count=%d, want 43", len(queries))
	}
}

func TestPinnedSchemaUsesTemporalTypes(t *testing.T) {
	schema, err := clickBenchSchema()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, "EventTime TIMESTAMP NOT NULL") || !strings.Contains(schema, "ClientEventTime TIMESTAMP NOT NULL") {
		t.Fatalf("pinned ClickBench schema is missing timestamp columns: %s", schema)
	}
}

func TestIntegration(t *testing.T) {
	connectionURL := os.Getenv("LIHACLOUD_BENCH_TEST_CLICKHOUSE_URL")
	if connectionURL == "" {
		t.Skip("LIHACLOUD_BENCH_TEST_CLICKHOUSE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	engine, err := New(ctx, connectionURL, "LIHACLOUD_BENCH_TEST_CLICKHOUSE_URL")
	if err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	resource, ownership, err := engine.EnsureDatabase(ctx, runID, "test", func(manifest.Resource) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := engine.Cleanup(context.Background(), runID, ownership); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	}()
	result := engine.RunClickBench(ctx, DefaultClickBenchSpec("quick", "lukewarm"))
	if result.Status != "success" || len(result.Metrics) < 8 || !resource.Owned {
		t.Fatalf("unexpected result: %+v", result)
	}
	if helper := os.Getenv("LIHACLOUD_BENCH_TEST_CLICKCANNON_PATH"); helper != "" {
		t.Setenv("LIHACLOUD_BENCH_CLICKCANNON_PATH", helper)
		cannon := engine.RunClickCannon(ctx, ClickCannonSpec{Profile: "quick", InsertDuration: 5 * time.Second, QueryDuration: 5 * time.Second, Users: 1, Comparable: false, ToolVersion: "dev"})
		if cannon.Status != "success" || len(cannon.Metrics) != 7 {
			t.Fatalf("unexpected ClickCannon result: %+v", cannon)
		}
		if cannon.Metrics[0].Value <= 0 || cannon.Metrics[3].Value <= 0 {
			t.Fatalf("ClickCannon reported zero throughput: %+v", cannon.Metrics)
		}
	}
}

func TestDatabaseIdentity(t *testing.T) {
	runID := "77aa4a1a-4f8f-42a9-816c-982e1cba80db"
	if !validDatabase("lihacloud_bench_77aa4a1a4f8f42a9816c982e1cba80db", runID) {
		t.Fatal("valid identity rejected")
	}
	if validDatabase("customer", runID) {
		t.Fatal("unowned database accepted")
	}
}

func TestDefaultSpec(t *testing.T) {
	quick := DefaultClickBenchSpec("quick", "lukewarm")
	if quick.Queries != 5 || quick.Trials != 1 || quick.Comparable {
		t.Fatalf("quick=%+v", quick)
	}
	standard := DefaultClickBenchSpec("standard", "true-cold")
	if standard.Queries != 43 || standard.Trials != 3 || !standard.Comparable {
		t.Fatalf("standard=%+v", standard)
	}
}
