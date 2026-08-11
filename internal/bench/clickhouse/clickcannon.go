package clickhouse

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/LIHA-Research/lihacloud-bench/internal/cache"
	"github.com/LIHA-Research/lihacloud-bench/internal/model"
	"github.com/LIHA-Research/lihacloud-bench/internal/redact"
	"go.yaml.in/yaml/v3"
)

const ClickCannonVersion = "v0.4.0"

type ClickCannonSpec struct {
	Profile        string
	InsertDuration time.Duration
	QueryDuration  time.Duration
	Users          int
	Comparable     bool
	ToolVersion    string
}

func DefaultClickCannonSpec(profile, toolVersion string) ClickCannonSpec {
	if profile == "quick" {
		return ClickCannonSpec{Profile: profile, InsertDuration: 60 * time.Second, QueryDuration: 60 * time.Second, Users: 2, Comparable: false, ToolVersion: toolVersion}
	}
	return ClickCannonSpec{Profile: profile, InsertDuration: 15 * time.Minute, QueryDuration: 15 * time.Minute, Users: 10, Comparable: true, ToolVersion: toolVersion}
}

func (engine *Engine) RunClickCannon(ctx context.Context, spec ClickCannonSpec) model.Benchmark {
	benchmark := model.Benchmark{
		ID: "clickhouse-clickcannon", Category: "clickhouse-clickcannon", Status: model.StatusSuccess,
		Engine: "clickcannon", EngineVersion: ClickCannonVersion, WorkloadVersion: "otel_demo-v1", Comparable: spec.Comparable,
		Parameters: map[string]any{"insert_duration_seconds": spec.InsertDuration.Seconds(), "query_duration_seconds": spec.QueryDuration.Seconds(), "users": spec.Users, "profile": "otel_demo", "data_type": "traces"},
		Metrics:    []model.Metric{}, Warnings: []string{"insert latency is estimated from completed batches because ClickCannon v0.4.0 does not emit an insert-latency sample"},
	}
	helper, err := ResolveClickCannon(ctx, spec.ToolVersion)
	if err != nil {
		return blockedClickCannon(benchmark, err)
	}
	database, err := engine.openDatabase()
	if err != nil {
		return fail(benchmark, "clickcannon_connection_failed", err)
	}
	defer database.Close()
	if _, err := database.ExecContext(ctx, traceTableDDL()); err != nil {
		return fail(benchmark, "clickcannon_schema_failed", err)
	}
	connection, err := clickCannonConnection(engine.connectionURL, engine.database)
	if err != nil {
		return blockedClickCannon(benchmark, err)
	}
	insertRunID := engine.runID + "-insert"
	insertConfig := clickCannonConfig(connection, engine.database, insertRunID, true, false, spec.InsertDuration, spec.Users)
	if _, err := runClickCannonPhase(ctx, helper, insertRunID, insertConfig, spec.InsertDuration, true); err != nil {
		return fail(benchmark, "clickcannon_insert_failed", err)
	}
	queryRunID := engine.runID + "-query"
	queryConfig := clickCannonConfig(connection, engine.database, queryRunID, false, true, spec.QueryDuration, spec.Users)
	queryOutput, err := runClickCannonPhase(ctx, helper, queryRunID, queryConfig, spec.QueryDuration+30*time.Second, false)
	if err != nil {
		return fail(benchmark, "clickcannon_query_failed", err)
	}
	rows := engine.metricMax(ctx, database, insertRunID, "insert_rows_total")
	bytes := engine.metricMax(ctx, database, insertRunID, "insert_bytes_uncompressed_total")
	batches := engine.metricMax(ctx, database, insertRunID, "insert_batches_total")
	seconds := spec.InsertDuration.Seconds()
	insertLatency := 0.0
	if batches > 0 {
		insertLatency = seconds * 1000 / batches
	}
	okQueries := engine.metricMax(ctx, database, queryRunID, "queries_ok_total")
	failedQueries := engine.metricMax(ctx, database, queryRunID, "queries_failed_total")
	if rows <= 0 || okQueries+failedQueries <= 0 {
		return fail(benchmark, "clickcannon_metrics_missing", fmt.Errorf("ClickCannon completed without usable insert or query metrics: %s", safeLogTail(queryOutput, connection.Password)))
	}
	if okQueries == 0 {
		return fail(benchmark, "clickcannon_queries_failed", fmt.Errorf("all ClickCannon queries failed: %s", safeLogTail(queryOutput, connection.Password)))
	}
	p50, p95, p99 := engine.queryLatencyPercentiles(ctx, database, queryRunID)
	errorRate := 0.0
	if okQueries+failedQueries > 0 {
		errorRate = failedQueries / (okQueries + failedQueries)
	}
	benchmark.Metrics = append(benchmark.Metrics,
		model.Metric{Name: "insert_rows_throughput", Value: rows / seconds, Unit: "rows/s", Direction: "higher"},
		model.Metric{Name: "insert_bytes_throughput", Value: bytes / seconds, Unit: "bytes/s", Direction: "higher"},
		model.Metric{Name: "insert_latency_avg", Value: insertLatency, Unit: "ms", Direction: "lower"},
		model.Metric{Name: "query_latency_p50", Value: p50 / 1000, Unit: "ms", Direction: "lower"},
		model.Metric{Name: "query_latency_p95", Value: p95 / 1000, Unit: "ms", Direction: "lower"},
		model.Metric{Name: "query_latency_p99", Value: p99 / 1000, Unit: "ms", Direction: "lower"},
		model.Metric{Name: "query_error_rate", Value: errorRate, Unit: "ratio", Direction: "lower"},
	)
	return benchmark
}

type cannonConnection struct {
	Address  string
	Secure   bool
	User     string
	Password string
	DSN      string
}

func clickCannonConnection(connectionURL, database string) (cannonConnection, error) {
	parsed, err := url.Parse(connectionURL)
	if err != nil {
		return cannonConnection{}, err
	}
	if parsed.Scheme != "clickhouse" && parsed.Scheme != "tcp" && parsed.Scheme != "clickhouses" {
		return cannonConnection{}, errors.New("ClickCannon requires a native clickhouse://, clickhouses://, or tcp:// URL")
	}
	user, password := "default", ""
	if parsed.User != nil {
		if parsed.User.Username() != "" {
			user = parsed.User.Username()
		}
		password, _ = parsed.User.Password()
	}
	secure := parsed.Scheme == "clickhouses" || parsed.Query().Get("secure") == "true"
	dsn := *parsed
	dsn.Scheme = "tcp"
	dsn.Path = "/" + database
	return cannonConnection{Address: parsed.Host, Secure: secure, User: user, Password: password, DSN: dsn.String()}, nil
}

func clickCannonConfig(connection cannonConnection, database, runID string, generate, users bool, duration time.Duration, userThreads int) map[string]any {
	return map[string]any{
		"app":      map[string]any{"name": "lihacloud-bench " + runID, "log_level": "info", "log_to_console": true, "log_to_file": false, "data_type": "traces", "seed": "lihacloud-bench-v1"},
		"generate": map[string]any{"enabled": generate, "threads": 2, "rows_per_block": 8192, "rows_per_second": 0, "reuse_blocks": true, "block_retirement_uses": 50, "profile": "otel_demo", "traces": map[string]any{"spans_per_trace_min": 3, "spans_per_trace_max": 12, "max_depth": 5, "duration_min_us": 1000, "duration_max_us": 5_000_000}},
		"disk":     map[string]any{"enabled": false},
		"insert":   map[string]any{"enabled": generate, "threads": 4, "batch_size": 100_000, "worker_retirement_batches": 100, "balance_nodes": false, "clickhouse": map[string]any{"address": connection.Address, "secure": connection.Secure, "user": connection.User, "password": connection.Password, "compression": "lz4", "database": database, "logs_table": "otel_logs", "traces_table": "otel_traces"}},
		"metrics":  map[string]any{"enabled": true, "clickhouse_dsn": connection.DSN, "database": database, "run_table": "clickcannon_runs", "metrics_table": "clickcannon_perf", "create_schema": true, "attributes": map[string]string{"tool": "lihacloud-bench"}},
		"user": map[string]any{"enabled": users, "duration": duration.String(), "threads": userThreads, "ramp_duration": min(duration/10, 30*time.Second).String(), "clickhouse_dsn": connection.DSN, "connections_per_thread": 1, "database": database, "table": "otel_traces", "workflows": []any{map[string]any{"type": "queries", "name": "LIHA OTel trace queries", "random": true, "think_time": map[string]string{"min": "0s", "max": "1s"}, "time_anchor": "now", "default_time_range": map[string]any{"type": "fixed", "value": "15m"}, "queries": []any{
			map[string]any{"name": "Recent trace count", "sql": "SELECT count() FROM {database:Identifier}.{table:Identifier} WHERE Timestamp >= {time_start:DateTime64(3)} AND Timestamp <= {time_end:DateTime64(3)}"},
			map[string]any{"name": "Services by spans", "sql": "SELECT ServiceName, count() FROM {database:Identifier}.{table:Identifier} WHERE Timestamp >= {time_start:DateTime64(3)} AND Timestamp <= {time_end:DateTime64(3)} GROUP BY ServiceName ORDER BY count() DESC LIMIT 20"},
			map[string]any{"name": "Duration percentiles", "sql": "SELECT quantile(0.5)(Duration), quantile(0.95)(Duration), quantile(0.99)(Duration) FROM {database:Identifier}.{table:Identifier} WHERE Timestamp >= {time_start:DateTime64(3)} AND Timestamp <= {time_end:DateTime64(3)}"},
		}}}},
	}
}

func runClickCannonPhase(ctx context.Context, helper, runID string, cfg map[string]any, duration time.Duration, expectTimeout bool) (string, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp("", ".lihacloud-bench-clickcannon-*.yaml")
	if err != nil {
		return "", err
	}
	filename := file.Name()
	defer os.Remove(filename)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return "", err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	phaseCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	command := exec.CommandContext(phaseCtx, helper, "--config", filename)
	command.Env = append(os.Environ(), "CLICKCANNON_RUN_ID="+runID)
	output, commandErr := command.CombinedOutput()
	if expectTimeout && errors.Is(phaseCtx.Err(), context.DeadlineExceeded) {
		return string(output), nil
	}
	if commandErr != nil {
		return string(output), fmt.Errorf("%w: %s", commandErr, redact.String(strings.TrimSpace(string(output))))
	}
	return string(output), nil
}

func safeLogTail(output string, secrets ...string) string {
	const limit = 4 * 1024
	if len(output) > limit {
		output = output[len(output)-limit:]
	}
	return redact.String(strings.TrimSpace(output), secrets...)
}

func traceTableDDL() string {
	return "CREATE TABLE otel_traces (Timestamp DateTime64(9), TraceId String, SpanId String, ParentSpanId String, TraceState String, SpanName LowCardinality(String), SpanKind LowCardinality(String), ServiceName LowCardinality(String), ResourceAttributes Map(String, String), ScopeName String, ScopeVersion String, SpanAttributes Map(String, String), Duration UInt64, StatusCode LowCardinality(String), StatusMessage String, `Events.Timestamp` Array(DateTime64(9)), `Events.Name` Array(String), `Events.Attributes` Array(Map(String, String)), `Links.TraceId` Array(String), `Links.SpanId` Array(String), `Links.TraceState` Array(String), `Links.Attributes` Array(Map(String, String))) ENGINE=MergeTree ORDER BY (ServiceName, SpanName, toUnixTimestamp(Timestamp), TraceId)"
}

func (engine *Engine) metricMax(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, runID, name string) float64 {
	var value float64
	_ = database.QueryRowContext(ctx, "SELECT coalesce(max(value), 0) FROM clickcannon_perf WHERE run_id=? AND metric_name=?", runID, name).Scan(&value)
	return value
}

func (engine *Engine) queryLatencyPercentiles(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, runID string) (float64, float64, float64) {
	var p50, p95, p99 float64
	_ = database.QueryRowContext(ctx, "SELECT quantileExact(0.50)(value), quantileExact(0.95)(value), quantileExact(0.99)(value) FROM clickcannon_perf WHERE run_id=? AND metric_name='query_latency_micros'", runID).Scan(&p50, &p95, &p99)
	return p50, p95, p99
}

func ResolveClickCannon(ctx context.Context, toolVersion string) (string, error) {
	if override := os.Getenv("LIHACLOUD_BENCH_CLICKCANNON_PATH"); override != "" {
		if info, err := os.Stat(override); err == nil && !info.IsDir() {
			return override, nil
		}
		return "", errors.New("LIHACLOUD_BENCH_CLICKCANNON_PATH does not name a helper binary")
	}
	if path, err := exec.LookPath("clickcannon"); err == nil {
		return path, nil
	}
	if toolVersion == "" || toolVersion == "dev" {
		return "", errors.New("ClickCannon helper is unavailable in a development build; set LIHACLOUD_BENCH_CLICKCANNON_PATH")
	}
	root, err := cache.Directory()
	if err != nil {
		return "", err
	}
	directory := filepath.Join(root, "helpers", toolVersion)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	asset := fmt.Sprintf("clickcannon_%s_%s_%s", toolVersion, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		asset += ".exe"
	}
	path := filepath.Join(directory, asset)
	checksumsURL := fmt.Sprintf("https://github.com/LIHA-Research/lihacloud-bench/releases/download/%s/checksums.txt", toolVersion)
	expected, err := fetchExpectedChecksum(ctx, checksumsURL, asset)
	if err != nil {
		return "", err
	}
	if actual, _, hashErr := hashFile(path); hashErr == nil && actual == expected {
		return path, nil
	}
	assetURL := fmt.Sprintf("https://github.com/LIHA-Research/lihacloud-bench/releases/download/%s/%s", toolVersion, asset)
	if err := downloadVerified(ctx, assetURL, path, expected); err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(path, 0o755)
	}
	return path, nil
}

func fetchExpectedChecksum(ctx context.Context, checksumsURL, asset string) (string, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, checksumsURL, nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download helper checksums: %s", response.Status)
	}
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 1024*1024))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && strings.TrimPrefix(fields[len(fields)-1], "*") == asset {
			return fields[0], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errors.New("release checksums do not contain the ClickCannon helper")
}

func downloadVerified(ctx context.Context, sourceURL, destination, expected string) error {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download ClickCannon helper: %s", response.Status)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".helper-*.part")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temporary, hasher), response.Body); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if actual := hex.EncodeToString(hasher.Sum(nil)); actual != expected {
		return errors.New("downloaded ClickCannon helper checksum mismatch")
	}
	return os.Rename(name, destination)
}

func blockedClickCannon(benchmark model.Benchmark, err error) model.Benchmark {
	benchmark.Status, benchmark.ErrorCode, benchmark.Error = model.StatusBlocked, "clickcannon_helper_unavailable", redact.String(err.Error())
	return benchmark
}
