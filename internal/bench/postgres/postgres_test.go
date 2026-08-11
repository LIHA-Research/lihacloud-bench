package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/LIHA-Research/lihacloud-bench/internal/manifest"
	"github.com/google/uuid"
)

func TestParseOutput(t *testing.T) {
	output := []byte(`transaction type: <builtin: TPC-B (sort of)>
number of clients: 4
number of transactions actually processed: 12345
latency average = 3.125 ms
tps = 1280.250000 (without initial connection time)`)
	parsed, err := ParseOutput(output)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.TPS != 1280.25 || parsed.LatencyMS != 3.125 || parsed.Transactions != 12345 {
		t.Fatalf("unexpected parsed output: %+v", parsed)
	}
}

func TestIntegration(t *testing.T) {
	connectionURL := os.Getenv("LIHACLOUD_BENCH_TEST_POSTGRES_URL")
	if connectionURL == "" {
		t.Skip("LIHACLOUD_BENCH_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	engine, err := New(ctx, connectionURL, "LIHACLOUD_BENCH_TEST_POSTGRES_URL")
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
	result := engine.Run(ctx, Spec{Profile: "quick", LogicalCPUs: 1, Scale: 1, Duration: time.Second, Clients: []int{1}, Comparable: false})
	if result.Status != "success" || len(result.Metrics) != 9 || !resource.Owned {
		t.Fatalf("unexpected result: %+v", result)
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
	quick := DefaultSpec("quick", 8)
	if quick.Scale != 10 || len(quick.Clients) != 2 || quick.Comparable {
		t.Fatalf("unexpected quick spec: %+v", quick)
	}
	standard := DefaultSpec("standard", 40)
	if standard.Scale != 100 || standard.Clients[2] != 64 || !standard.Comparable {
		t.Fatalf("unexpected standard spec: %+v", standard)
	}
}
