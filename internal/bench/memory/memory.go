package memory

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/LIHA-Research/lihacloud-bench/internal/model"
	"github.com/LIHA-Research/lihacloud-bench/internal/statistics"
)

const mebibyte = 1024 * 1024

type Spec struct {
	Size       int
	Trials     int
	Workers    int
	Comparable bool
}

func DefaultSpec(profile string, availableBytes uint64, logicalCPUs int) Spec {
	size := availableBytes / 4
	if size > 1024*mebibyte {
		size = 1024 * mebibyte
	}
	if size < 16*mebibyte {
		size = 16 * mebibyte
	}
	trials, comparable := 3, true
	if profile == "quick" {
		if size > 128*mebibyte {
			size = 128 * mebibyte
		}
		trials, comparable = 1, false
	}
	return Spec{Size: int(size), Trials: trials, Workers: max(1, logicalCPUs), Comparable: comparable}
}

func Run(ctx context.Context, spec Spec) model.Benchmark {
	benchmark := model.Benchmark{
		ID: "memory", Category: "memory", Status: model.StatusSuccess,
		Engine: "builtin-memory", WorkloadVersion: "sequential-v1", Comparable: spec.Comparable,
		Parameters: map[string]any{"buffer_bytes": spec.Size, "trials": spec.Trials, "multi_workers": spec.Workers},
		Metrics:    []model.Metric{}, Warnings: []string{},
	}
	if spec.Size < 1 || spec.Trials < 1 || spec.Workers < 1 {
		benchmark.Status, benchmark.ErrorCode, benchmark.Error = model.StatusFailed, "invalid_workload", "memory workload parameters must be positive"
		return benchmark
	}
	source := make([]byte, spec.Size)
	destination := make([]byte, spec.Size)
	for index := range source {
		source[index] = byte(index*31 + 17)
	}
	for _, operation := range []string{"read", "write", "copy"} {
		for _, workerCount := range []int{1, spec.Workers} {
			values := make([]float64, 0, spec.Trials)
			for trial := 0; trial < spec.Trials; trial++ {
				if err := ctx.Err(); err != nil {
					benchmark.Status, benchmark.ErrorCode, benchmark.Error = model.StatusFailed, "workload_failed", err.Error()
					return benchmark
				}
				throughput, err := measure(ctx, operation, source, destination, workerCount)
				if err != nil {
					benchmark.Status, benchmark.ErrorCode, benchmark.Error = model.StatusFailed, "workload_failed", err.Error()
					return benchmark
				}
				values = append(values, throughput)
			}
			mean, variation, _, _, _ := statistics.Summary(values)
			mode := "single"
			if workerCount != 1 {
				mode = "multi"
			}
			benchmark.Metrics = append(benchmark.Metrics,
				model.Metric{Name: fmt.Sprintf("%s_%s_throughput", mode, operation), Value: mean, Unit: "MiB/s", Direction: "higher"},
				model.Metric{Name: fmt.Sprintf("%s_%s_variation", mode, operation), Value: variation, Unit: "ratio", Direction: "lower"},
			)
		}
	}
	return benchmark
}

var readSink uint64

func measure(ctx context.Context, operation string, source, destination []byte, workers int) (float64, error) {
	start := time.Now()
	var wait sync.WaitGroup
	partials := make([]uint64, workers)
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		startOffset := len(source) * worker / workers
		endOffset := len(source) * (worker + 1) / workers
		go func(worker, startOffset, endOffset int) {
			defer wait.Done()
			switch operation {
			case "read":
				var sum uint64
				for _, value := range source[startOffset:endOffset] {
					sum += uint64(value)
				}
				partials[worker] = sum
			case "write":
				for index := startOffset; index < endOffset; index++ {
					destination[index] = byte(index + worker)
				}
			case "copy":
				copy(destination[startOffset:endOffset], source[startOffset:endOffset])
			}
		}(worker, startOffset, endOffset)
	}
	wait.Wait()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	for _, value := range partials {
		readSink += value
	}
	runtime.KeepAlive(source)
	runtime.KeepAlive(destination)
	elapsed := time.Since(start)
	if elapsed <= 0 {
		// Some Windows clocks do not advance during very small test buffers.
		// Production buffers are much larger, but keep the metric finite and
		// deterministic at the timer's smallest representable interval.
		elapsed = time.Nanosecond
	}
	return float64(len(source)) / mebibyte / elapsed.Seconds(), nil
}
