package disk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/LIHA-Research/lihacloud-bench/internal/model"
)

const mebibyte = 1024 * 1024

type Spec struct {
	Directory  string
	Duration   time.Duration
	FileSize   int64
	Comparable bool
}

type Detection struct {
	Path    string
	Version string
}

func DefaultSpec(profile, directory string) Spec {
	duration, size, comparable := 60*time.Second, int64(1024*mebibyte), true
	if profile == "quick" {
		duration, size, comparable = 5*time.Second, 64*mebibyte, false
	}
	return Spec{Directory: directory, Duration: duration, FileSize: size, Comparable: comparable}
}

func Detect(ctx context.Context) Detection {
	path, err := exec.LookPath("fio")
	if err != nil {
		return Detection{}
	}
	command := exec.CommandContext(ctx, path, "--version")
	output, err := command.Output()
	if err != nil {
		return Detection{Path: path}
	}
	return Detection{Path: path, Version: strings.TrimSpace(string(output))}
}

func Run(ctx context.Context, spec Spec) model.Benchmark {
	detection := Detect(ctx)
	if detection.Path != "" {
		return runFio(ctx, spec, detection)
	}
	return runBuiltin(ctx, spec)
}

func runFio(ctx context.Context, spec Spec, detection Detection) model.Benchmark {
	benchmark := base(spec)
	benchmark.Engine = "fio"
	benchmark.EngineVersion = detection.Version
	file, err := temporaryPath(spec.Directory)
	if err != nil {
		return fail(benchmark, "temporary_file", err)
	}
	path := file.Name()
	file.Close()
	defer os.Remove(path)
	arguments := []string{"--output-format=json", "--filename=" + path, "--size=" + strconv.FormatInt(spec.FileSize, 10), "--time_based=1", "--runtime=" + strconv.Itoa(max(1, int(spec.Duration.Seconds()))), "--iodepth=32", "--group_reporting=1"}
	if runtime.GOOS == "linux" {
		arguments = append(arguments, "--direct=1")
	}
	jobs := []struct{ name, rw, block string }{
		{"sequential-read", "read", "1M"}, {"sequential-write", "write", "1M"},
		{"random-read", "randread", "4k"}, {"random-write", "randwrite", "4k"},
	}
	for index, job := range jobs {
		if index > 0 {
			arguments = append(arguments, "--stonewall")
		}
		arguments = append(arguments, "--name="+job.name, "--rw="+job.rw, "--bs="+job.block)
	}
	command := exec.CommandContext(ctx, detection.Path, arguments...)
	output, err := command.Output()
	if err != nil {
		var exitError *exec.ExitError
		if ok := errorAs(err, &exitError); ok {
			return fail(benchmark, "fio_failed", fmt.Errorf("fio: %s", strings.TrimSpace(string(exitError.Stderr))))
		}
		return fail(benchmark, "fio_failed", err)
	}
	metrics, err := ParseFio(output)
	if err != nil {
		return fail(benchmark, "fio_parse_failed", err)
	}
	benchmark.Metrics = metrics
	return benchmark
}

// errorAs is kept small here to make command failure handling easy to test on
// all supported platforms without exporting implementation details.
func errorAs(err error, target any) bool {
	switch value := target.(type) {
	case **exec.ExitError:
		exitError, ok := err.(*exec.ExitError)
		if ok {
			*value = exitError
		}
		return ok
	default:
		return false
	}
}

type fioDocument struct {
	Jobs []struct {
		Name  string       `json:"jobname"`
		Read  fioOperation `json:"read"`
		Write fioOperation `json:"write"`
	} `json:"jobs"`
}

type fioOperation struct {
	BandwidthBytes float64 `json:"bw_bytes"`
	IOPS           float64 `json:"iops"`
	Latency        struct {
		Percentile map[string]float64 `json:"percentile"`
	} `json:"clat_ns"`
}

func ParseFio(output []byte) ([]model.Metric, error) {
	var document fioDocument
	decoder := json.NewDecoder(bytes.NewReader(output))
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode fio JSON: %w", err)
	}
	if len(document.Jobs) != 4 {
		return nil, fmt.Errorf("expected four fio jobs, got %d", len(document.Jobs))
	}
	metrics := make([]model.Metric, 0, 16)
	for _, job := range document.Jobs {
		operation := job.Read
		if operation.BandwidthBytes == 0 && operation.IOPS == 0 {
			operation = job.Write
		}
		prefix := strings.ReplaceAll(job.Name, "-", "_")
		metrics = append(metrics,
			model.Metric{Name: prefix + "_throughput", Value: operation.BandwidthBytes / mebibyte, Unit: "MiB/s", Direction: "higher"},
			model.Metric{Name: prefix + "_iops", Value: operation.IOPS, Unit: "ops/s", Direction: "higher"},
		)
		for _, percentile := range []struct{ source, suffix string }{{"50.000000", "p50"}, {"95.000000", "p95"}, {"99.000000", "p99"}} {
			if value, ok := operation.Latency.Percentile[percentile.source]; ok {
				metrics = append(metrics, model.Metric{Name: prefix + "_latency_" + percentile.suffix, Value: value / 1e6, Unit: "ms", Direction: "lower"})
			}
		}
	}
	return metrics, nil
}

func runBuiltin(ctx context.Context, spec Spec) model.Benchmark {
	benchmark := base(spec)
	benchmark.Engine = "builtin-buffered"
	benchmark.Comparable = false
	benchmark.Warnings = append(benchmark.Warnings, "fio was not found; buffered filesystem results are not comparable")
	file, err := temporaryPath(spec.Directory)
	if err != nil {
		return fail(benchmark, "temporary_file", err)
	}
	path := file.Name()
	defer os.Remove(path)
	defer file.Close()
	if err := file.Truncate(spec.FileSize); err != nil {
		return fail(benchmark, "prepare_file", err)
	}
	buffer := make([]byte, mebibyte)
	for index := range buffer {
		buffer[index] = byte(index*17 + 5)
	}
	for _, workload := range []struct {
		name      string
		write     bool
		random    bool
		blockSize int
	}{
		{"sequential_read", false, false, mebibyte}, {"sequential_write", true, false, mebibyte},
		{"random_read", false, true, 4096}, {"random_write", true, true, 4096},
	} {
		throughput, operations, err := measureBuiltin(ctx, file, buffer[:workload.blockSize], spec.FileSize, spec.Duration, workload.write, workload.random)
		if err != nil {
			return fail(benchmark, "builtin_io_failed", err)
		}
		benchmark.Metrics = append(benchmark.Metrics,
			model.Metric{Name: workload.name + "_throughput", Value: throughput, Unit: "MiB/s", Direction: "higher"},
			model.Metric{Name: workload.name + "_iops", Value: operations, Unit: "ops/s", Direction: "higher"},
		)
	}
	return benchmark
}

func measureBuiltin(ctx context.Context, file *os.File, buffer []byte, fileSize int64, duration time.Duration, write, randomAccess bool) (float64, float64, error) {
	deadline := time.Now().Add(duration)
	start := time.Now()
	var bytesProcessed uint64
	var operations uint64
	offset := int64(0)
	reader := bufio.NewReaderSize(file, len(buffer))
	writer := bufio.NewWriterSize(file, len(buffer))
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		if randomAccess {
			limit := max(int64(1), fileSize/int64(len(buffer)))
			offset = rand.Int64N(limit) * int64(len(buffer))
		}
		var count int
		var err error
		if write && randomAccess {
			count, err = file.WriteAt(buffer, offset)
		} else if !write && randomAccess {
			count, err = file.ReadAt(buffer, offset)
		} else if write {
			count, err = writer.Write(buffer)
		} else {
			count, err = io.ReadFull(reader, buffer)
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
				return 0, 0, seekErr
			}
			reader.Reset(file)
			writer.Reset(file)
			continue
		}
		if err != nil {
			return 0, 0, err
		}
		bytesProcessed += uint64(count)
		operations++
		offset += int64(count)
		if offset+int64(len(buffer)) > fileSize {
			offset = 0
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				return 0, 0, err
			}
			reader.Reset(file)
			writer.Reset(file)
		}
	}
	if err := writer.Flush(); err != nil {
		return 0, 0, err
	}
	if write {
		if err := file.Sync(); err != nil {
			return 0, 0, err
		}
	}
	elapsed := time.Since(start).Seconds()
	return float64(bytesProcessed) / mebibyte / elapsed, float64(operations) / elapsed, nil
}

func temporaryPath(directory string) (*os.File, error) {
	if directory == "" {
		directory = os.TempDir()
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("create disk directory: %w", err)
	}
	return os.CreateTemp(directory, ".lihacloud-bench-disk-*")
}

func base(spec Spec) model.Benchmark {
	return model.Benchmark{
		ID: "disk", Category: "disk", Status: model.StatusSuccess, WorkloadVersion: "fio-v1",
		Comparable: spec.Comparable, Parameters: map[string]any{"duration_seconds_per_workload": spec.Duration.Seconds(), "file_bytes": spec.FileSize},
		Metrics: []model.Metric{}, Warnings: []string{},
	}
}

func fail(benchmark model.Benchmark, code string, err error) model.Benchmark {
	benchmark.Status, benchmark.ErrorCode, benchmark.Error = model.StatusFailed, code, err.Error()
	return benchmark
}
