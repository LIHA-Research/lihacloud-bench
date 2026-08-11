package clickhouse

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/LIHA-Research/lihacloud-bench/internal/cache"
	"github.com/LIHA-Research/lihacloud-bench/internal/manifest"
	"github.com/LIHA-Research/lihacloud-bench/internal/model"
	"github.com/LIHA-Research/lihacloud-bench/internal/redact"
)

const (
	ClickBenchCommit  = "e2fe0d3f4803cbf0a47068a9c7530b059a32d195"
	markerTable       = "lihacloud_bench_marker"
	fullDatasetURL    = "https://datasets.clickhouse.com/hits_compatible/hits.parquet"
	quickDatasetURL   = "https://datasets.clickhouse.com/hits_compatible/athena_partitioned/hits_0.parquet"
	fullDatasetBytes  = int64(14_779_976_446)
	quickDatasetBytes = int64(122_446_530)
	fullDatasetETag   = `"6b028bb94eecf0ff4e6cde62a0f8fa48-829"`
	quickDatasetETag  = `"843c108848a3929260d44588b39ec1b6-6"`
)

//go:embed assets/clickbench-queries.sql
var assets embed.FS

type Engine struct {
	connectionURL string
	urlEnv        string
	database      string
	runID         string
	version       string
}

type ClickBenchSpec struct {
	Profile    string
	CacheMode  string
	Queries    int
	Trials     int
	Comparable bool
}

type Outcome struct {
	Benchmark model.Benchmark
	Resource  model.Resource
	Manifest  manifest.Resource
}

func New(ctx context.Context, connectionURL, urlEnv string) (*Engine, error) {
	if strings.TrimSpace(connectionURL) == "" {
		return nil, errors.New("ClickHouse URL environment variable is not set")
	}
	db, err := sql.Open("clickhouse", connectionURL)
	if err != nil {
		return nil, fmt.Errorf("open ClickHouse connection: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("connect to ClickHouse: %s", redact.String(err.Error(), connectionURL))
	}
	var version string
	if err := db.QueryRowContext(ctx, "SELECT version()").Scan(&version); err != nil {
		return nil, fmt.Errorf("read ClickHouse version: %w", err)
	}
	return &Engine{connectionURL: connectionURL, urlEnv: urlEnv, version: version}, nil
}

func DefaultClickBenchSpec(profile, cacheMode string) ClickBenchSpec {
	if profile == "quick" {
		return ClickBenchSpec{Profile: profile, CacheMode: cacheMode, Queries: 5, Trials: 1, Comparable: false}
	}
	return ClickBenchSpec{Profile: profile, CacheMode: cacheMode, Queries: 43, Trials: 3, Comparable: true}
}

func (engine *Engine) EnsureDatabase(ctx context.Context, runID, toolVersion string, record func(manifest.Resource) error) (model.Resource, manifest.Resource, error) {
	engine.runID = runID
	engine.database = "lihacloud_bench_" + strings.ReplaceAll(runID, "-", "")
	resource := model.Resource{Kind: "clickhouse_database", Name: engine.database, Owned: true, CleanupStatus: "pending"}
	manifestResource := manifest.Resource{Kind: resource.Kind, Name: engine.database, Marker: markerTable, Properties: map[string]string{"url_env": engine.urlEnv}}
	if !validDatabase(engine.database, runID) {
		return model.Resource{}, manifest.Resource{}, errors.New("generated ClickHouse database identity is invalid")
	}
	admin, err := sql.Open("clickhouse", engine.connectionURL)
	if err != nil {
		return resource, manifestResource, err
	}
	defer admin.Close()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+quoteIdentifier(engine.database)); err != nil {
		resource.Owned = false
		return resource, manifestResource, fmt.Errorf("create ClickHouse database: %w", err)
	}
	database, err := engine.openDatabase()
	if err != nil {
		return resource, manifestResource, err
	}
	defer database.Close()
	if _, err := database.ExecContext(ctx, "CREATE TABLE "+quoteIdentifier(markerTable)+" (run_id String, tool_version String, created_at DateTime64(3) DEFAULT now64(3)) ENGINE=TinyLog"); err != nil {
		return resource, manifestResource, fmt.Errorf("create ClickHouse marker: %w", err)
	}
	if _, err := database.ExecContext(ctx, "INSERT INTO "+quoteIdentifier(markerTable)+" (run_id, tool_version) VALUES (?, ?)", runID, toolVersion); err != nil {
		return resource, manifestResource, fmt.Errorf("write ClickHouse marker: %w", err)
	}
	if err := record(manifestResource); err != nil {
		return resource, manifestResource, err
	}
	return resource, manifestResource, nil
}

func (engine *Engine) RunClickBench(ctx context.Context, spec ClickBenchSpec) model.Benchmark {
	benchmark := model.Benchmark{
		ID: "clickhouse-clickbench", Category: "clickhouse-clickbench", Status: model.StatusSuccess,
		Engine: "clickhouse-go", EngineVersion: engine.version, WorkloadVersion: ClickBenchCommit, Comparable: spec.Comparable,
		Parameters: map[string]any{"rows": map[bool]int{true: 1_000_000, false: 99_997_497}[spec.Profile == "quick"], "queries": spec.Queries, "trials": spec.Trials, "cache_mode": spec.CacheMode},
		Metrics:    []model.Metric{}, Warnings: []string{},
	}
	datasetURL, expectedBytes, expectedETag, filename := fullDatasetURL, fullDatasetBytes, fullDatasetETag, "hits.parquet"
	if spec.Profile == "quick" {
		datasetURL, expectedBytes, expectedETag, filename = quickDatasetURL, quickDatasetBytes, quickDatasetETag, "hits_0.parquet"
	}
	cachePath, checksum, err := cacheDataset(ctx, datasetURL, filename, expectedBytes, expectedETag)
	if err != nil {
		return fail(benchmark, "clickbench_dataset_failed", err)
	}
	benchmark.Parameters["dataset_sha256"] = checksum
	benchmark.Parameters["dataset_cache_file"] = filepath.Base(cachePath)
	benchmark.Warnings = append(benchmark.Warnings, "ClickHouse fetches the verified dataset URL server-side, so server egress is included in load time")
	database, err := engine.openDatabase()
	if err != nil {
		return fail(benchmark, "clickbench_connection_failed", err)
	}
	defer database.Close()
	createSQL := "CREATE TABLE hits ENGINE=MergeTree ORDER BY (CounterID, EventDate, UserID, EventTime, WatchID) SETTINGS allow_nullable_key=1 AS SELECT * FROM url(" + quoteLiteral(datasetURL) + ", Parquet) LIMIT 0"
	if _, err := database.ExecContext(ctx, createSQL); err != nil {
		return fail(benchmark, "clickbench_create_failed", err)
	}
	loadStarted := time.Now()
	if _, err := database.ExecContext(ctx, "INSERT INTO hits SELECT * FROM url("+quoteLiteral(datasetURL)+", Parquet)"); err != nil {
		return fail(benchmark, "clickbench_load_failed", err)
	}
	benchmark.Metrics = append(benchmark.Metrics, model.Metric{Name: "load_time", Value: time.Since(loadStarted).Seconds(), Unit: "s", Direction: "lower"})
	var rows, dataBytes uint64
	if err := database.QueryRowContext(ctx, "SELECT count() FROM hits").Scan(&rows); err != nil {
		return fail(benchmark, "clickbench_count_failed", err)
	}
	if err := database.QueryRowContext(ctx, "SELECT coalesce(sum(bytes_on_disk), 0) FROM system.parts WHERE active AND database=? AND table='hits'", engine.database).Scan(&dataBytes); err != nil {
		benchmark.Warnings = append(benchmark.Warnings, "ClickHouse data size could not be read from system.parts")
	}
	benchmark.Metrics = append(benchmark.Metrics,
		model.Metric{Name: "loaded_rows", Value: float64(rows), Unit: "rows", Direction: "neutral"},
		model.Metric{Name: "data_size", Value: float64(dataBytes), Unit: "bytes", Direction: "lower"},
	)
	queries, err := clickBenchQueries()
	if err != nil {
		return fail(benchmark, "clickbench_queries_failed", err)
	}
	if spec.Queries > len(queries) {
		return fail(benchmark, "clickbench_queries_failed", errors.New("pinned ClickBench query count is incomplete"))
	}
	coldAvailable := false
	if spec.CacheMode == "true-cold" {
		if _, dropErr := database.ExecContext(ctx, "SYSTEM DROP FILESYSTEM CACHE"); dropErr == nil {
			coldAvailable = true
		} else {
			benchmark.Warnings = append(benchmark.Warnings, "managed ClickHouse did not permit cache eviction; first query run is labeled lukewarm")
		}
	}
	for queryIndex := 0; queryIndex < spec.Queries; queryIndex++ {
		for trial := 0; trial < spec.Trials; trial++ {
			phase := "hot"
			if trial == 0 {
				if coldAvailable {
					phase = "cold"
				} else {
					phase = "lukewarm"
				}
			}
			started := time.Now()
			rows, queryErr := database.QueryContext(ctx, queries[queryIndex])
			if queryErr != nil {
				return fail(benchmark, "clickbench_query_failed", fmt.Errorf("query %d: %w", queryIndex+1, queryErr))
			}
			for rows.Next() {
			}
			queryErr = errors.Join(rows.Err(), rows.Close())
			if queryErr != nil {
				return fail(benchmark, "clickbench_query_failed", fmt.Errorf("query %d: %w", queryIndex+1, queryErr))
			}
			benchmark.Metrics = append(benchmark.Metrics, model.Metric{Name: fmt.Sprintf("query_%02d_%s_run_%d", queryIndex+1, phase, trial+1), Value: float64(time.Since(started).Microseconds()) / 1000, Unit: "ms", Direction: "lower"})
		}
	}
	return benchmark
}

func (engine *Engine) Cleanup(ctx context.Context, runID string, resource manifest.Resource) error {
	if resource.Kind != "clickhouse_database" || resource.Marker != markerTable || !validDatabase(resource.Name, runID) || resource.Properties["url_env"] != engine.urlEnv {
		return errors.New("cleanup refused: local ClickHouse manifest ownership mismatch")
	}
	databaseURL, err := databaseURL(engine.connectionURL, resource.Name)
	if err != nil {
		return err
	}
	database, err := sql.Open("clickhouse", databaseURL)
	if err != nil {
		return err
	}
	defer database.Close()
	var marker string
	if err := database.QueryRowContext(ctx, "SELECT run_id FROM "+quoteIdentifier(markerTable)+" LIMIT 1").Scan(&marker); err != nil {
		return fmt.Errorf("verify ClickHouse marker: %w", err)
	}
	if marker != runID {
		return errors.New("cleanup refused: remote ClickHouse marker mismatch")
	}
	admin, err := sql.Open("clickhouse", engine.connectionURL)
	if err != nil {
		return err
	}
	defer admin.Close()
	if _, err := admin.ExecContext(ctx, "DROP DATABASE "+quoteIdentifier(resource.Name)); err != nil {
		return fmt.Errorf("drop owned ClickHouse database: %w", err)
	}
	return nil
}

func (engine *Engine) openDatabase() (*sql.DB, error) {
	value, err := databaseURL(engine.connectionURL, engine.database)
	if err != nil {
		return nil, err
	}
	return sql.Open("clickhouse", value)
}

func databaseURL(connectionURL, database string) (string, error) {
	parsed, err := url.Parse(connectionURL)
	if err != nil {
		return "", err
	}
	parsed.Path = "/" + database
	return parsed.String(), nil
}

func clickBenchQueries() ([]string, error) {
	data, err := assets.ReadFile("assets/clickbench-queries.sql")
	if err != nil {
		return nil, err
	}
	queries := []string{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		query := strings.TrimSpace(scanner.Text())
		if query != "" {
			queries = append(queries, strings.TrimSuffix(query, ";"))
		}
	}
	return queries, scanner.Err()
}

func cacheDataset(ctx context.Context, datasetURL, filename string, expectedBytes int64, expectedETag string) (string, string, error) {
	root, err := cache.Directory()
	if err != nil {
		return "", "", err
	}
	directory := filepath.Join(root, "clickbench", ClickBenchCommit)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", "", err
	}
	path := filepath.Join(directory, filename)
	checksumPath := path + ".sha256"
	if checksumData, readErr := os.ReadFile(checksumPath); readErr == nil {
		checksum := strings.TrimSpace(string(checksumData))
		actual, size, hashErr := hashFile(path)
		if hashErr == nil && size == expectedBytes && actual == checksum {
			return path, checksum, nil
		}
	}
	temporary, err := os.CreateTemp(directory, ".dataset-*.part")
	if err != nil {
		return "", "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, datasetURL, nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		temporary.Close()
		return "", "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength != expectedBytes || response.Header.Get("ETag") != expectedETag {
		temporary.Close()
		return "", "", fmt.Errorf("dataset identity mismatch: status=%s bytes=%d etag=%q", response.Status, response.ContentLength, response.Header.Get("ETag"))
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hasher), response.Body)
	if copyErr != nil || written != expectedBytes {
		temporary.Close()
		return "", "", errors.Join(copyErr, fmt.Errorf("downloaded %d of %d bytes", written, expectedBytes))
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return "", "", err
	}
	if err := temporary.Close(); err != nil {
		return "", "", err
	}
	checksum := hex.EncodeToString(hasher.Sum(nil))
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(checksumPath, []byte(checksum+"\n"), 0o644); err != nil {
		return "", "", err
	}
	return path, checksum, nil
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", 0, err
	}
	var hasher hash.Hash = sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), info.Size(), nil
}

func validDatabase(name, runID string) bool {
	return name == "lihacloud_bench_"+strings.ReplaceAll(runID, "-", "")
}
func quoteIdentifier(value string) string { return "`" + strings.ReplaceAll(value, "`", "``") + "`" }
func quoteLiteral(value string) string    { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func fail(benchmark model.Benchmark, code string, err error) model.Benchmark {
	benchmark.Status, benchmark.ErrorCode, benchmark.Error = model.StatusFailed, code, redact.String(err.Error())
	return benchmark
}
