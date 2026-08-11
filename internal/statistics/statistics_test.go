package statistics

import (
	"math"
	"testing"
)

func TestSummary(t *testing.T) {
	mean, variation, p50, p95, p99 := Summary([]float64{1, 2, 3, 4, 5})
	if mean != 3 || math.Abs(variation-.4714045) > .000001 {
		t.Fatalf("mean=%f variation=%f", mean, variation)
	}
	if p50 != 3 || p95 != 4.8 || p99 != 4.96 {
		t.Fatalf("percentiles=%f,%f,%f", p50, p95, p99)
	}
}

func TestSummaryEmpty(t *testing.T) {
	mean, variation, p50, p95, p99 := Summary(nil)
	if mean+variation+p50+p95+p99 != 0 {
		t.Fatal("empty sample returned non-zero values")
	}
}
