package runner

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/LIHA-Research/lihacloud-bench/internal/bench/cpu"
	"github.com/LIHA-Research/lihacloud-bench/internal/bench/disk"
	"github.com/LIHA-Research/lihacloud-bench/internal/bench/memory"
	"github.com/LIHA-Research/lihacloud-bench/internal/bench/s3bench"
	"github.com/LIHA-Research/lihacloud-bench/internal/config"
	"github.com/LIHA-Research/lihacloud-bench/internal/host"
	"github.com/LIHA-Research/lihacloud-bench/internal/manifest"
	"github.com/LIHA-Research/lihacloud-bench/internal/model"
	"github.com/LIHA-Research/lihacloud-bench/internal/redact"
	resultio "github.com/LIHA-Research/lihacloud-bench/internal/result"
)

var knownCategories = []string{
	"cpu", "memory", "disk", "object-s3", "postgres-pgbench",
	"clickhouse-clickbench", "clickhouse-clickcannon", "network-peer",
	"network-iperf", "network-internet",
}

func ConfiguredCategories(cfg config.Config) []string {
	categories := []string{"cpu", "memory", "disk"}
	if cfg.S3.Enabled {
		categories = append(categories, "object-s3")
	}
	if cfg.Postgres.Enabled {
		categories = append(categories, "postgres-pgbench")
	}
	if cfg.ClickHouse.Enabled {
		categories = append(categories, "clickhouse-clickbench", "clickhouse-clickcannon")
	}
	if cfg.Network.Peer.Enabled {
		categories = append(categories, "network-peer")
	}
	if cfg.Network.IPerf.Enabled {
		categories = append(categories, "network-iperf")
	}
	if cfg.Network.Internet.Enabled {
		categories = append(categories, "network-internet")
	}
	return categories
}

func SelectCategories(cfg config.Config, only []string) ([]string, error) {
	if len(only) == 0 {
		return ConfiguredCategories(cfg), nil
	}
	selected := make([]string, 0, len(only))
	for _, category := range only {
		category = strings.TrimSpace(category)
		if category == "" {
			continue
		}
		if !slices.Contains(knownCategories, category) {
			return nil, fmt.Errorf("unknown benchmark category %q", category)
		}
		if !slices.Contains(selected, category) {
			selected = append(selected, category)
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("--only did not select any categories")
	}
	return selected, nil
}

func Plan(ctx context.Context, cfg config.Config, categories []string, runID string) (model.Plan, error) {
	plan := model.Plan{Categories: slices.Clone(categories), Resources: []model.PlannedResource{}, Engines: []model.PlannedEngine{}}
	quick := cfg.Profile == "quick"
	for _, category := range categories {
		switch category {
		case "cpu":
			plan.Engines = append(plan.Engines, model.PlannedEngine{Category: category, Name: "builtin-sha256", State: "ready"})
			if quick {
				plan.EstimatedDurationSeconds += 10
			} else {
				plan.EstimatedDurationSeconds += 180
			}
		case "memory":
			plan.Engines = append(plan.Engines, model.PlannedEngine{Category: category, Name: "builtin-memory", State: "ready"})
			if quick {
				plan.EstimatedDurationSeconds += 5
			} else {
				plan.EstimatedDurationSeconds += 30
			}
		case "disk":
			detection := disk.Detect(ctx)
			engine, version := "builtin-buffered", ""
			if detection.Path != "" {
				engine, version = "fio", detection.Version
			}
			plan.Engines = append(plan.Engines, model.PlannedEngine{Category: category, Name: engine, Version: version, State: "ready"})
			if quick {
				plan.EstimatedDurationSeconds += 20
			} else {
				plan.EstimatedDurationSeconds += 240
			}
		case "object-s3":
			name := cfg.S3.BucketPrefix + "-" + runID
			if len(name) > 63 {
				return model.Plan{}, errors.New("generated S3 bucket name exceeds 63 characters")
			}
			plan.Resources = append(plan.Resources, model.PlannedResource{Kind: "s3_bucket", Name: name})
			plan.Engines = append(plan.Engines, model.PlannedEngine{Category: category, Name: "aws-sdk-go-v2", Version: "v1", State: "ready"})
			if quick {
				plan.EstimatedTransferBytes += 2*64*1024*1024 + 4*100*4096
				plan.EstimatedRequests += 404
				plan.EstimatedDurationSeconds += 60
			} else {
				plan.EstimatedTransferBytes += 6*1024*1024*1024 + 4*10_000*4096
				plan.EstimatedRequests += 40_008
				plan.EstimatedDurationSeconds += 900
			}
		case "network-internet":
			plan.ExternalDataSharing = true
			plan.Engines = append(plan.Engines, model.PlannedEngine{Category: category, Name: "ndt7", Version: "v0.10.1", State: "ready"})
		default:
			plan.Engines = append(plan.Engines, model.PlannedEngine{Category: category, Name: category, State: "ready"})
		}
	}
	return plan, nil
}

type Execution struct {
	Result model.Result
	Path   string
}

func Execute(ctx context.Context, cfg config.Config, categories []string, runID string, tool model.ToolInfo) (Execution, error) {
	plan, err := Plan(ctx, cfg, categories, runID)
	if err != nil {
		return Execution{}, err
	}
	started := time.Now().UTC()
	value := model.Result{
		SchemaVersion: model.SchemaVersion, Tool: tool,
		Run:  model.RunInfo{ID: runID, Profile: cfg.Profile, Status: "running", StartedAt: started},
		Host: host.Collect(ctx, cfg.HostLabel), Plan: plan,
		Benchmarks: []model.Benchmark{}, Resources: []model.Resource{}, Warnings: []string{},
	}
	store, err := manifest.NewStore()
	if err != nil {
		return Execution{}, err
	}
	manifestValue := manifest.New(runID)
	persist := func() (string, error) { return resultio.WriteAtomic(cfg.Output, value) }
	path, err := persist()
	if err != nil {
		return Execution{}, err
	}
	var s3Engine *s3bench.Engine
	for _, category := range categories {
		if ctx.Err() != nil {
			break
		}
		var benchmark model.Benchmark
		switch category {
		case "cpu":
			benchmark = cpu.Run(ctx, cpu.DefaultSpec(cfg.Profile, value.Host.LogicalCPUs))
		case "memory":
			benchmark = memory.Run(ctx, memory.DefaultSpec(cfg.Profile, host.AvailableMemory(ctx), value.Host.LogicalCPUs))
		case "disk":
			benchmark = disk.Run(ctx, disk.DefaultSpec(cfg.Profile, cfg.Disk.Path))
		case "object-s3":
			s3Engine, err = s3bench.New(ctx, cfg.S3)
			if err != nil {
				benchmark = blocked(category, "aws-sdk-go-v2", "s3_configuration", err)
				break
			}
			outcome := s3Engine.Run(ctx, s3bench.DefaultSpec(cfg.Profile, runID, tool.Version), func(resource manifest.Resource) error {
				manifestValue.Resources = append(manifestValue.Resources, resource)
				return store.Save(manifestValue)
			})
			benchmark = outcome.Benchmark
			if outcome.Resource.Owned {
				value.Resources = append(value.Resources, outcome.Resource)
			}
		default:
			benchmark = blocked(category, category, "engine_unavailable", errors.New("benchmark engine is not implemented in this build"))
		}
		value.Benchmarks = append(value.Benchmarks, benchmark)
		path, err = persist()
		if err != nil {
			return Execution{}, err
		}
	}

	cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelCleanup()
	for index := range value.Resources {
		if cfg.KeepResources {
			value.Resources[index].CleanupStatus = "kept"
			continue
		}
		var cleanupErr error
		found := false
		for _, resource := range manifestValue.Resources {
			if resource.Kind == value.Resources[index].Kind && resource.Name == value.Resources[index].Name {
				found = true
				switch resource.Kind {
				case "s3_bucket":
					if s3Engine == nil {
						cleanupErr = errors.New("S3 client unavailable for cleanup")
					} else {
						cleanupErr = s3Engine.Cleanup(cleanupContext, runID, resource)
					}
				}
			}
		}
		if !found {
			cleanupErr = errors.New("cleanup refused: resource is not recorded in the local manifest")
		}
		if cleanupErr != nil {
			value.Resources[index].CleanupStatus, value.Resources[index].Error = "failed", cleanupErr.Error()
		} else {
			value.Resources[index].CleanupStatus = "deleted"
		}
	}
	if !cfg.KeepResources && cleanupComplete(value.Resources) {
		_ = store.Delete(runID)
	}
	ended := time.Now().UTC()
	value.Run.EndedAt = &ended
	value.Run.Status = status(value, ctx.Err())
	path, err = persist()
	if err != nil {
		return Execution{}, err
	}
	return Execution{Result: value, Path: path}, nil
}

func blocked(category, engine, code string, err error) model.Benchmark {
	return model.Benchmark{ID: category, Category: category, Status: model.StatusBlocked, Engine: engine, Comparable: false, Metrics: []model.Metric{}, Warnings: []string{}, ErrorCode: code, Error: redact.String(err.Error())}
}

func cleanupComplete(resources []model.Resource) bool {
	for _, resource := range resources {
		if resource.CleanupStatus != "deleted" {
			return false
		}
	}
	return true
}

func status(value model.Result, contextError error) string {
	if contextError != nil {
		return "interrupted"
	}
	partial := false
	for _, benchmark := range value.Benchmarks {
		if benchmark.Status != model.StatusSuccess && benchmark.Status != model.StatusSkipped {
			partial = true
		}
	}
	for _, resource := range value.Resources {
		if resource.CleanupStatus == "failed" {
			partial = true
		}
	}
	if partial {
		return "partial"
	}
	return "success"
}

func Cleanup(ctx context.Context, runID string) ([]model.Resource, error) {
	store, err := manifest.NewStore()
	if err != nil {
		return nil, err
	}
	value, err := store.Load(runID)
	if err != nil {
		return nil, err
	}
	results := make([]model.Resource, 0, len(value.Resources))
	var failures []error
	for _, resource := range value.Resources {
		result := model.Resource{Kind: resource.Kind, Name: resource.Name, Owned: true, CleanupStatus: "failed"}
		switch resource.Kind {
		case "s3_bucket":
			pathStyle, _ := strconv.ParseBool(resource.Properties["path_style"])
			engine, engineErr := s3bench.New(ctx, config.S3Config{Endpoint: resource.Properties["endpoint"], Region: resource.Properties["region"], PathStyle: pathStyle, BucketPrefix: resource.Properties["bucket_prefix"]})
			if engineErr == nil {
				engineErr = engine.Cleanup(ctx, runID, resource)
			}
			if engineErr != nil {
				result.Error = engineErr.Error()
				failures = append(failures, engineErr)
			} else {
				result.CleanupStatus = "deleted"
			}
		default:
			result.Error = "unknown manifest resource kind"
			failures = append(failures, errors.New(result.Error))
		}
		results = append(results, result)
	}
	if len(failures) == 0 {
		_ = store.Delete(runID)
	}
	return results, errors.Join(failures...)
}
