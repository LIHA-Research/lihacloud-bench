package disk

import (
	"context"
	"testing"
	"time"
)

func TestBuiltin(t *testing.T) {
	result := runBuiltin(context.Background(), Spec{Directory: t.TempDir(), Duration: 10 * time.Millisecond, FileSize: 1024 * 1024})
	if result.Status != "success" || result.Engine != "builtin-buffered" || result.Comparable {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.Metrics) != 8 {
		t.Fatalf("metrics=%d", len(result.Metrics))
	}
}

func TestParseFio(t *testing.T) {
	data := []byte(`{"jobs":[
	{"jobname":"sequential-read","read":{"bw_bytes":1048576,"iops":1,"clat_ns":{"percentile":{"50.000000":1000000}}}},
	{"jobname":"sequential-write","write":{"bw_bytes":2097152,"iops":2,"clat_ns":{"percentile":{}}}},
	{"jobname":"random-read","read":{"bw_bytes":4096,"iops":10,"clat_ns":{"percentile":{}}}},
	{"jobname":"random-write","write":{"bw_bytes":8192,"iops":20,"clat_ns":{"percentile":{}}}}
	]}`)
	metrics, err := ParseFio(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 9 || metrics[0].Value != 1 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
}
