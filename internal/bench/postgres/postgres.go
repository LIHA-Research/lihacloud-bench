package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/LIHA-Research/lihacloud-bench/internal/manifest"
	"github.com/LIHA-Research/lihacloud-bench/internal/model"
	"github.com/LIHA-Research/lihacloud-bench/internal/redact"
)

const markerTable = "lihacloud_bench_marker"

type Detection struct {
	PgbenchPath string
	PsqlPath    string
	Version     string
}

type Engine struct {
	detection Detection
	baseURL   string
	urlEnv    string
	database  string
	runID     string
}

type Spec struct {
	Profile     string
	LogicalCPUs int
	Scale       int
	Warmup      time.Duration
	Duration    time.Duration
	Clients     []int
	Comparable  bool
}

type Outcome struct {
	Benchmark model.Benchmark
	Resource  model.Resource
	Manifest  manifest.Resource
}

func Detect(ctx context.Context) Detection {
	detection := Detection{}
	detection.PgbenchPath, _ = exec.LookPath("pgbench")
	detection.PsqlPath, _ = exec.LookPath("psql")
	if detection.PgbenchPath != "" {
		output, _ := exec.CommandContext(ctx, detection.PgbenchPath, "--version").Output()
		detection.Version = strings.TrimSpace(string(output))
	}
	return detection
}

func New(ctx context.Context, connectionURL, urlEnv string) (*Engine, error) {
	detection := Detect(ctx)
	if detection.PgbenchPath == "" || detection.PsqlPath == "" {
		return nil, errors.New("pgbench and psql must both be installed")
	}
	parsed, err := url.Parse(connectionURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		return nil, errors.New("PostgreSQL URL environment variable must contain a postgres:// or postgresql:// URL")
	}
	return &Engine{detection: detection, baseURL: connectionURL, urlEnv: urlEnv}, nil
}

func DefaultSpec(profile string, logicalCPUs int) Spec {
	logicalCPUs = max(1, logicalCPUs)
	if profile == "quick" {
		return Spec{Profile: profile, LogicalCPUs: logicalCPUs, Scale: 10, Duration: 30 * time.Second, Clients: unique([]int{1, logicalCPUs}), Comparable: false}
	}
	return Spec{Profile: profile, LogicalCPUs: logicalCPUs, Scale: 100, Warmup: 30 * time.Second, Duration: 120 * time.Second, Clients: unique([]int{1, logicalCPUs, min(2*logicalCPUs, 64)}), Comparable: true}
}

func (engine *Engine) EnsureDatabase(ctx context.Context, runID, toolVersion string, record func(manifest.Resource) error) (model.Resource, manifest.Resource, error) {
	engine.runID = runID
	engine.database = "lihacloud_bench_" + strings.ReplaceAll(runID, "-", "")
	resource := model.Resource{Kind: "postgres_database", Name: engine.database, Owned: true, CleanupStatus: "pending"}
	manifestResource := manifest.Resource{Kind: resource.Kind, Name: engine.database, Marker: markerTable, Properties: map[string]string{"url_env": engine.urlEnv}}
	if !validDatabase(engine.database, runID) {
		return model.Resource{}, manifest.Resource{}, errors.New("generated PostgreSQL database identity is invalid")
	}
	if output, err := engine.psql(ctx, engine.baseURL, "CREATE DATABASE "+quoteIdentifier(engine.database)); err != nil {
		resource.Owned = false
		return resource, manifestResource, commandError(err, output)
	}
	databaseURL, err := databaseURL(engine.baseURL, engine.database)
	if err != nil {
		return resource, manifestResource, err
	}
	markerSQL := "CREATE TABLE " + quoteIdentifier(markerTable) + " (run_id text PRIMARY KEY, tool_version text NOT NULL, created_at timestamptz NOT NULL DEFAULT now()); " +
		"INSERT INTO " + quoteIdentifier(markerTable) + " (run_id, tool_version) VALUES (" + quoteLiteral(runID) + ", " + quoteLiteral(toolVersion) + "); " +
		"COMMENT ON TABLE " + quoteIdentifier(markerTable) + " IS " + quoteLiteral("lihacloud-bench owned resource "+runID)
	if output, err := engine.psql(ctx, databaseURL, markerSQL); err != nil {
		return resource, manifestResource, commandError(err, output)
	}
	if err := record(manifestResource); err != nil {
		return resource, manifestResource, err
	}
	return resource, manifestResource, nil
}

func (engine *Engine) Run(ctx context.Context, spec Spec) model.Benchmark {
	benchmark := model.Benchmark{
		ID: "postgres-pgbench", Category: "postgres-pgbench", Status: model.StatusSuccess,
		Engine: "pgbench", EngineVersion: engine.detection.Version, WorkloadVersion: "pgbench-builtin-v1", Comparable: spec.Comparable,
		Parameters: map[string]any{"scale": spec.Scale, "warmup_seconds": spec.Warmup.Seconds(), "duration_seconds": spec.Duration.Seconds(), "clients": spec.Clients, "workloads": []string{"tpcb-like", "select-only", "simple-update"}},
		Metrics:    []model.Metric{}, Warnings: []string{},
	}
	databaseURL, err := databaseURL(engine.baseURL, engine.database)
	if err != nil {
		return fail(benchmark, "invalid_database_url", err)
	}
	if output, err := engine.pgbench(ctx, databaseURL, "-i", "-s", strconv.Itoa(spec.Scale)); err != nil {
		return fail(benchmark, "pgbench_initialize_failed", commandError(err, output))
	}
	workloads := []struct{ name, builtin string }{{"tpcb_like", "tpcb-like"}, {"select_only", "select-only"}, {"simple_update", "simple-update"}}
	for _, workload := range workloads {
		for _, clients := range spec.Clients {
			threads := min(max(1, clients), max(1, spec.LogicalCPUs))
			baseArguments := []string{"-n", "-c", strconv.Itoa(clients), "-j", strconv.Itoa(threads), "-b", workload.builtin}
			if spec.Warmup > 0 {
				arguments := append(append([]string{}, baseArguments...), "-T", strconv.Itoa(max(1, int(spec.Warmup.Seconds()))))
				if output, err := engine.pgbench(ctx, databaseURL, arguments...); err != nil {
					return fail(benchmark, "pgbench_warmup_failed", commandError(err, output))
				}
			}
			arguments := append(append([]string{}, baseArguments...), "-T", strconv.Itoa(max(1, int(spec.Duration.Seconds()))))
			output, err := engine.pgbench(ctx, databaseURL, arguments...)
			if err != nil {
				return fail(benchmark, "pgbench_run_failed", commandError(err, output))
			}
			parsed, err := ParseOutput(output)
			if err != nil {
				return fail(benchmark, "pgbench_parse_failed", err)
			}
			prefix := fmt.Sprintf("%s_c%d", workload.name, clients)
			benchmark.Metrics = append(benchmark.Metrics,
				model.Metric{Name: prefix + "_tps", Value: parsed.TPS, Unit: "transactions/s", Direction: "higher"},
				model.Metric{Name: prefix + "_latency_avg", Value: parsed.LatencyMS, Unit: "ms", Direction: "lower"},
				model.Metric{Name: prefix + "_transactions", Value: float64(parsed.Transactions), Unit: "transactions", Direction: "higher"},
			)
		}
	}
	return benchmark
}

type ParsedOutput struct {
	TPS          float64
	LatencyMS    float64
	Transactions uint64
}

var (
	tpsPattern          = regexp.MustCompile(`(?m)^tps = ([0-9.]+)`)
	latencyPattern      = regexp.MustCompile(`(?m)^latency average = ([0-9.]+) ms`)
	transactionsPattern = regexp.MustCompile(`(?m)^number of transactions actually processed: ([0-9]+)`)
)

func ParseOutput(output []byte) (ParsedOutput, error) {
	text := string(output)
	tpsMatch, latencyMatch, transactionMatch := tpsPattern.FindStringSubmatch(text), latencyPattern.FindStringSubmatch(text), transactionsPattern.FindStringSubmatch(text)
	if len(tpsMatch) != 2 || len(latencyMatch) != 2 || len(transactionMatch) != 2 {
		return ParsedOutput{}, errors.New("pgbench output is missing TPS, latency, or transaction count")
	}
	tps, tpsErr := strconv.ParseFloat(tpsMatch[1], 64)
	latency, latencyErr := strconv.ParseFloat(latencyMatch[1], 64)
	transactions, transactionErr := strconv.ParseUint(transactionMatch[1], 10, 64)
	if err := errors.Join(tpsErr, latencyErr, transactionErr); err != nil {
		return ParsedOutput{}, fmt.Errorf("parse pgbench metrics: %w", err)
	}
	return ParsedOutput{TPS: tps, LatencyMS: latency, Transactions: transactions}, nil
}

func (engine *Engine) Cleanup(ctx context.Context, runID string, resource manifest.Resource) error {
	if resource.Kind != "postgres_database" || resource.Marker != markerTable || !validDatabase(resource.Name, runID) || resource.Properties["url_env"] != engine.urlEnv {
		return errors.New("cleanup refused: local PostgreSQL manifest ownership mismatch")
	}
	databaseURL, err := databaseURL(engine.baseURL, resource.Name)
	if err != nil {
		return err
	}
	output, err := engine.psql(ctx, databaseURL, "SELECT run_id FROM "+quoteIdentifier(markerTable)+" LIMIT 1")
	if err != nil {
		return fmt.Errorf("verify PostgreSQL marker: %w", commandError(err, output))
	}
	if strings.TrimSpace(string(output)) != runID {
		return errors.New("cleanup refused: remote PostgreSQL marker mismatch")
	}
	_, err = engine.psql(ctx, engine.baseURL, "DROP DATABASE "+quoteIdentifier(resource.Name)+" WITH (FORCE)")
	if err != nil {
		return fmt.Errorf("drop owned PostgreSQL database: %w", err)
	}
	return nil
}

func (engine *Engine) pgbench(ctx context.Context, connectionURL string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, engine.detection.PgbenchPath, arguments...)
	command.Env = append(os.Environ(), pgEnvironment(connectionURL)...)
	return command.CombinedOutput()
}

func (engine *Engine) psql(ctx context.Context, connectionURL, query string) ([]byte, error) {
	command := exec.CommandContext(ctx, engine.detection.PsqlPath, "-X", "--no-psqlrc", "-v", "ON_ERROR_STOP=1", "-At", "-c", query)
	command.Env = append(os.Environ(), pgEnvironment(connectionURL)...)
	return command.CombinedOutput()
}

func pgEnvironment(connectionURL string) []string {
	parsed, err := url.Parse(connectionURL)
	if err != nil {
		return nil
	}
	values := []string{"PGHOST=" + parsed.Hostname(), "PGDATABASE=" + strings.TrimPrefix(parsed.Path, "/")}
	if port := parsed.Port(); port != "" {
		values = append(values, "PGPORT="+port)
	}
	if parsed.User != nil {
		values = append(values, "PGUSER="+parsed.User.Username())
		if password, present := parsed.User.Password(); present {
			values = append(values, "PGPASSWORD="+password)
		}
	}
	for _, name := range []string{"sslmode", "sslcert", "sslkey", "sslrootcert"} {
		if value := parsed.Query().Get(name); value != "" {
			values = append(values, "PG"+strings.ToUpper(name)+"="+value)
		}
	}
	return values
}

func commandError(err error, output []byte) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, redact.String(message))
}

func databaseURL(connectionURL, database string) (string, error) {
	parsed, err := url.Parse(connectionURL)
	if err != nil {
		return "", err
	}
	parsed.Path = "/" + database
	return parsed.String(), nil
}

func validDatabase(name, runID string) bool {
	return name == "lihacloud_bench_"+strings.ReplaceAll(runID, "-", "")
}
func quoteIdentifier(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }
func quoteLiteral(value string) string    { return `'` + strings.ReplaceAll(value, `'`, `''`) + `'` }

func unique(values []int) []int {
	result := make([]int, 0, len(values))
	for _, value := range values {
		found := false
		for _, current := range result {
			if current == value {
				found = true
				break
			}
		}
		if !found {
			result = append(result, value)
		}
	}
	return result
}

func fail(benchmark model.Benchmark, code string, err error) model.Benchmark {
	benchmark.Status, benchmark.ErrorCode, benchmark.Error = model.StatusFailed, code, redact.String(err.Error())
	return benchmark
}
