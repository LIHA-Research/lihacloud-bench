package runner

import (
	"context"
	"testing"

	"github.com/LIHA-Research/lihacloud-bench/internal/config"
	"github.com/LIHA-Research/lihacloud-bench/internal/model"
)

func TestSelectCategories(t *testing.T) {
	cfg := config.Defaults()
	cfg.S3.Enabled = true
	categories, err := SelectCategories(cfg, nil)
	if err != nil || len(categories) != 4 || categories[3] != "object-s3" {
		t.Fatalf("categories=%v err=%v", categories, err)
	}
	if _, err := SelectCategories(cfg, []string{"unknown"}); err == nil {
		t.Fatal("unknown category accepted")
	}
}

func TestQuickPlan(t *testing.T) {
	cfg := config.Defaults()
	cfg.Profile = "quick"
	cfg.S3.Enabled = true
	plan, err := Plan(context.Background(), cfg, ConfiguredCategories(cfg), "77aa4a1a-4f8f-42a9-816c-982e1cba80db")
	if err != nil {
		t.Fatal(err)
	}
	if plan.EstimatedRequests != 404 || plan.EstimatedTransferBytes == 0 || len(plan.Resources) != 1 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestStatus(t *testing.T) {
	value := model.Result{Benchmarks: []model.Benchmark{{Status: model.StatusSuccess}}}
	if status(value, nil) != "success" {
		t.Fatal("expected success")
	}
	value.Benchmarks[0].Status = model.StatusFailed
	if status(value, nil) != "partial" {
		t.Fatal("expected partial")
	}
}
