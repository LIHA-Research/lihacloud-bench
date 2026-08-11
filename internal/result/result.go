package result

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/LIHA-Research/lihacloud-bench/internal/model"
)

// WriteAtomic persists a complete result without exposing a partially-written
// JSON document to readers.
func WriteAtomic(outputDir string, value model.Result) (string, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("create output directory: %w", err)
	}
	path := filepath.Join(outputDir, value.Run.ID+".json")
	temporary, err := os.CreateTemp(outputDir, ".result-*.json")
	if err != nil {
		return "", fmt.Errorf("create result: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		temporary.Close()
		return "", fmt.Errorf("encode result: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return "", fmt.Errorf("sync result: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close result: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", fmt.Errorf("publish result: %w", err)
	}
	return path, nil
}

func PrintSummary(writer io.Writer, value model.Result, path string) {
	_, _ = fmt.Fprintf(writer, "\n%-18s %-10s %-20s %s\n", "BENCHMARK", "STATUS", "ENGINE", "PRIMARY METRIC")
	for _, benchmark := range value.Benchmarks {
		primary := "-"
		if len(benchmark.Metrics) > 0 {
			metric := benchmark.Metrics[0]
			primary = fmt.Sprintf("%.2f %s", metric.Value, metric.Unit)
		}
		_, _ = fmt.Fprintf(writer, "%-18s %-10s %-20s %s\n", benchmark.ID, strings.ToUpper(string(benchmark.Status)), benchmark.Engine, primary)
	}
	_, _ = fmt.Fprintf(writer, "\nRun %s: %s\nResult: %s\n", value.Run.ID, value.Run.Status, path)
}
