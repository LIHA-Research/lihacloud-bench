package cpu

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LIHA-Research/lihacloud-bench/internal/model"
	"github.com/LIHA-Research/lihacloud-bench/internal/statistics"
)

const mebibyte = 1024 * 1024

type Spec struct {
	Duration   time.Duration
	Trials     int
	BlockSize  int
	Workers    int
	Comparable bool
}

func DefaultSpec(profile string, logicalCPUs int) Spec {
	duration := 30 * time.Second
	trials := 3
	comparable := true
	if profile == "quick" {
		duration = 5 * time.Second
		trials = 1
		comparable = false
	}
	if logicalCPUs < 1 {
		logicalCPUs = 1
	}
	return Spec{Duration: duration, Trials: trials, BlockSize: 4 * mebibyte, Workers: logicalCPUs, Comparable: comparable}
}

func Run(ctx context.Context, spec Spec) model.Benchmark {
	benchmark := model.Benchmark{
		ID: "cpu", Category: "cpu", Status: model.StatusSuccess,
		Engine: "builtin-sha256", WorkloadVersion: "sha256-v1", Comparable: spec.Comparable,
		Parameters: map[string]any{"duration_seconds": spec.Duration.Seconds(), "trials": spec.Trials, "block_bytes": spec.BlockSize, "multi_workers": spec.Workers, "affinity_applied": false},
		Metrics:    []model.Metric{}, Warnings: []string{"CPU affinity is not applied on this platform-neutral workload"},
	}
	if spec.Duration <= 0 || spec.Trials < 1 || spec.BlockSize < 1 || spec.Workers < 1 {
		benchmark.Status, benchmark.ErrorCode, benchmark.Error = model.StatusFailed, "invalid_workload", "CPU workload parameters must be positive"
		return benchmark
	}
	single, err := trials(ctx, spec, 1)
	if err != nil {
		return failed(benchmark, err)
	}
	multi, err := trials(ctx, spec, spec.Workers)
	if err != nil {
		return failed(benchmark, err)
	}
	singleMean, singleVariation, _, _, _ := statistics.Summary(single)
	multiMean, multiVariation, _, _, _ := statistics.Summary(multi)
	scale := 0.0
	if singleMean > 0 {
		scale = multiMean / singleMean
	}
	benchmark.Metrics = []model.Metric{
		{Name: "single_throughput", Value: singleMean, Unit: "MiB/s", Direction: "higher"},
		{Name: "single_variation", Value: singleVariation, Unit: "ratio", Direction: "lower"},
		{Name: "multi_throughput", Value: multiMean, Unit: "MiB/s", Direction: "higher"},
		{Name: "multi_variation", Value: multiVariation, Unit: "ratio", Direction: "lower"},
		{Name: "multi_core_scale", Value: scale, Unit: "ratio", Direction: "higher"},
	}
	return benchmark
}

func failed(benchmark model.Benchmark, err error) model.Benchmark {
	benchmark.Status = model.StatusFailed
	benchmark.ErrorCode = "workload_failed"
	benchmark.Error = err.Error()
	return benchmark
}

func trials(ctx context.Context, spec Spec, workers int) ([]float64, error) {
	values := make([]float64, 0, spec.Trials)
	for trial := 0; trial < spec.Trials; trial++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value, err := measure(ctx, spec.Duration, spec.BlockSize, workers, trial)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

var digestSink [sha256.Size]byte

func measure(ctx context.Context, duration time.Duration, blockSize, workers, trial int) (float64, error) {
	trialCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	start := time.Now()
	var bytesProcessed atomic.Uint64
	var wait sync.WaitGroup
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(worker int) {
			defer wait.Done()
			block := deterministicBlock(blockSize, uint64(trial*workers+worker))
			var local [sha256.Size]byte
			for {
				select {
				case <-trialCtx.Done():
					if worker == 0 {
						digestSink = local
					}
					return
				default:
					local = sha256.Sum256(block)
					bytesProcessed.Add(uint64(len(block)))
					block[worker%len(block)] ^= local[0]
				}
			}
		}(worker)
	}
	wait.Wait()
	elapsed := time.Since(start)
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return float64(bytesProcessed.Load()) / mebibyte / elapsed.Seconds(), nil
}

func deterministicBlock(size int, seed uint64) []byte {
	block := make([]byte, size)
	for offset := 0; offset+8 <= len(block); offset += 8 {
		seed = seed*6364136223846793005 + 1442695040888963407
		binary.LittleEndian.PutUint64(block[offset:], seed)
	}
	return block
}

func LogicalWorkers() int { return max(1, runtime.NumCPU()) }
