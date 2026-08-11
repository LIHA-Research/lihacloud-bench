package s3bench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LIHA-Research/lihacloud-bench/internal/config"
	"github.com/LIHA-Research/lihacloud-bench/internal/manifest"
	"github.com/LIHA-Research/lihacloud-bench/internal/model"
	"github.com/LIHA-Research/lihacloud-bench/internal/statistics"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const (
	markerKey = ".lihacloud-bench/owner.json"
	mebibyte  = 1024 * 1024
)

type Client interface {
	CreateBucket(context.Context, *s3.CreateBucketInput, ...func(*s3.Options)) (*s3.CreateBucketOutput, error)
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObjects(context.Context, *s3.DeleteObjectsInput, ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
	DeleteBucket(context.Context, *s3.DeleteBucketInput, ...func(*s3.Options)) (*s3.DeleteBucketOutput, error)
}

type Engine struct {
	client Client
	config config.S3Config
}

type Spec struct {
	RunID       string
	ToolVersion string
	Profile     string
	LargeBytes  int64
	LargeTrials int
	SmallCount  int
	Comparable  bool
}

type Outcome struct {
	Benchmark model.Benchmark
	Resource  model.Resource
	Manifest  manifest.Resource
}

type marker struct {
	SchemaVersion int       `json:"schema_version"`
	RunID         string    `json:"run_id"`
	ToolVersion   string    `json:"tool_version"`
	CreatedAt     time.Time `json:"created_at"`
}

func New(ctx context.Context, cfg config.S3Config) (*Engine, error) {
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("load AWS configuration: %w", err)
	}
	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.UsePathStyle = cfg.PathStyle
		if cfg.Endpoint != "" {
			options.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	})
	return &Engine{client: client, config: cfg}, nil
}

func NewWithClient(client Client, cfg config.S3Config) *Engine {
	return &Engine{client: client, config: cfg}
}

func DefaultSpec(profile, runID, toolVersion string) Spec {
	spec := Spec{RunID: runID, ToolVersion: toolVersion, Profile: profile, LargeBytes: 1024 * mebibyte, LargeTrials: 3, SmallCount: 10_000, Comparable: true}
	if profile == "quick" {
		spec.LargeBytes, spec.LargeTrials, spec.SmallCount, spec.Comparable = 64*mebibyte, 1, 100, false
	}
	return spec
}

func (engine *Engine) Run(ctx context.Context, spec Spec, record func(manifest.Resource) error) Outcome {
	bucket := engine.config.BucketPrefix + "-" + spec.RunID
	outcome := Outcome{
		Benchmark: model.Benchmark{ID: "object-s3", Category: "object-s3", Status: model.StatusSuccess, Engine: "aws-sdk-go-v2", WorkloadVersion: "s3-v1", Comparable: spec.Comparable, Parameters: map[string]any{"large_object_bytes": spec.LargeBytes, "large_trials": spec.LargeTrials, "small_object_bytes": 4096, "small_object_count": spec.SmallCount, "concurrency": []int{1, 32}}, Metrics: []model.Metric{}, Warnings: []string{}},
		Resource:  model.Resource{Kind: "s3_bucket", Name: bucket, Owned: true, CleanupStatus: "pending"},
		Manifest:  manifest.Resource{Kind: "s3_bucket", Name: bucket, Marker: markerKey, Properties: map[string]string{"endpoint": engine.config.Endpoint, "region": engine.config.Region, "path_style": fmt.Sprint(engine.config.PathStyle), "bucket_prefix": engine.config.BucketPrefix}},
	}
	if err := validateBucket(bucket, engine.config.BucketPrefix, spec.RunID); err != nil {
		return fail(outcome, "invalid_resource_name", err)
	}
	createInput := &s3.CreateBucketInput{Bucket: aws.String(bucket)}
	if engine.config.Region != "us-east-1" {
		createInput.CreateBucketConfiguration = &types.CreateBucketConfiguration{LocationConstraint: types.BucketLocationConstraint(engine.config.Region)}
	}
	if _, err := engine.client.CreateBucket(ctx, createInput); err != nil {
		outcome.Resource.Owned = false
		return fail(outcome, "create_bucket_failed", err)
	}
	markerValue := marker{SchemaVersion: manifest.SchemaVersion, RunID: spec.RunID, ToolVersion: spec.ToolVersion, CreatedAt: time.Now().UTC()}
	markerData, _ := json.Marshal(markerValue)
	if _, err := engine.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(markerKey), Body: bytes.NewReader(markerData)}); err != nil {
		return fail(outcome, "write_marker_failed", err)
	}
	if err := record(outcome.Manifest); err != nil {
		return fail(outcome, "save_manifest_failed", err)
	}
	largeFile, err := os.CreateTemp("", ".lihacloud-bench-s3-*")
	if err != nil {
		return fail(outcome, "create_payload_failed", err)
	}
	defer os.Remove(largeFile.Name())
	defer largeFile.Close()
	if err := largeFile.Truncate(spec.LargeBytes); err != nil {
		return fail(outcome, "create_payload_failed", err)
	}
	uploads, downloads := []float64{}, []float64{}
	for trial := 0; trial < spec.LargeTrials; trial++ {
		if _, err := largeFile.Seek(0, io.SeekStart); err != nil {
			return fail(outcome, "payload_seek_failed", err)
		}
		started := time.Now()
		if _, err := engine.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String("large.bin"), Body: largeFile, ContentLength: aws.Int64(spec.LargeBytes)}); err != nil {
			return fail(outcome, "large_upload_failed", err)
		}
		uploads = append(uploads, float64(spec.LargeBytes)/mebibyte/time.Since(started).Seconds())
		started = time.Now()
		object, err := engine.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String("large.bin")})
		if err != nil {
			return fail(outcome, "large_download_failed", err)
		}
		_, copyErr := io.Copy(io.Discard, object.Body)
		closeErr := object.Body.Close()
		if copyErr != nil || closeErr != nil {
			return fail(outcome, "large_download_failed", errors.Join(copyErr, closeErr))
		}
		downloads = append(downloads, float64(spec.LargeBytes)/mebibyte/time.Since(started).Seconds())
	}
	uploadMean, uploadVariation, _, _, _ := statistics.Summary(uploads)
	downloadMean, downloadVariation, _, _, _ := statistics.Summary(downloads)
	outcome.Benchmark.Metrics = append(outcome.Benchmark.Metrics,
		model.Metric{Name: "large_upload_throughput", Value: uploadMean, Unit: "MiB/s", Direction: "higher"},
		model.Metric{Name: "large_upload_variation", Value: uploadVariation, Unit: "ratio", Direction: "lower"},
		model.Metric{Name: "large_download_throughput", Value: downloadMean, Unit: "MiB/s", Direction: "higher"},
		model.Metric{Name: "large_download_variation", Value: downloadVariation, Unit: "ratio", Direction: "lower"},
	)
	for _, concurrency := range []int{1, 32} {
		put := engine.smallOperation(ctx, bucket, spec.SmallCount, concurrency, true)
		get := engine.smallOperation(ctx, bucket, spec.SmallCount, concurrency, false)
		outcome.Benchmark.Metrics = append(outcome.Benchmark.Metrics, operationMetrics(fmt.Sprintf("small_put_c%d", concurrency), put)...)
		outcome.Benchmark.Metrics = append(outcome.Benchmark.Metrics, operationMetrics(fmt.Sprintf("small_get_c%d", concurrency), get)...)
		if put.errors > 0 || get.errors > 0 {
			outcome.Benchmark.Status = model.StatusFailed
			outcome.Benchmark.ErrorCode = "small_object_errors"
			outcome.Benchmark.Error = "one or more small-object requests failed"
		}
	}
	return outcome
}

type operationResult struct {
	duration  time.Duration
	latencies []float64
	errors    uint64
	count     int
}

func (engine *Engine) smallOperation(ctx context.Context, bucket string, count, concurrency int, write bool) operationResult {
	result := operationResult{count: count, latencies: make([]float64, count)}
	payload := make([]byte, 4096)
	jobs := make(chan int)
	var failures atomic.Uint64
	var wait sync.WaitGroup
	wait.Add(concurrency)
	started := time.Now()
	for worker := 0; worker < concurrency; worker++ {
		go func() {
			defer wait.Done()
			for index := range jobs {
				operationStarted := time.Now()
				key := fmt.Sprintf("small/%05d.bin", index)
				var err error
				if write {
					_, err = engine.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(payload), ContentLength: aws.Int64(int64(len(payload)))})
				} else {
					var object *s3.GetObjectOutput
					object, err = engine.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
					if err == nil {
						_, err = io.Copy(io.Discard, object.Body)
						err = errors.Join(err, object.Body.Close())
					}
				}
				result.latencies[index] = float64(time.Since(operationStarted).Microseconds()) / 1000
				if err != nil {
					failures.Add(1)
				}
			}
		}()
	}
	for index := 0; index < count; index++ {
		select {
		case jobs <- index:
		case <-ctx.Done():
			close(jobs)
			wait.Wait()
			result.duration, result.errors = time.Since(started), failures.Load()+uint64(count-index)
			return result
		}
	}
	close(jobs)
	wait.Wait()
	result.duration, result.errors = time.Since(started), failures.Load()
	return result
}

func operationMetrics(prefix string, result operationResult) []model.Metric {
	_, _, p50, p95, p99 := statistics.Summary(result.latencies)
	seconds := result.duration.Seconds()
	if seconds == 0 {
		seconds = 1
	}
	return []model.Metric{
		{Name: prefix + "_throughput", Value: float64(result.count) / seconds, Unit: "ops/s", Direction: "higher"},
		{Name: prefix + "_latency_p50", Value: p50, Unit: "ms", Direction: "lower"},
		{Name: prefix + "_latency_p95", Value: p95, Unit: "ms", Direction: "lower"},
		{Name: prefix + "_latency_p99", Value: p99, Unit: "ms", Direction: "lower"},
		{Name: prefix + "_error_rate", Value: float64(result.errors) / float64(max(1, result.count)), Unit: "ratio", Direction: "lower"},
	}
}

func fail(outcome Outcome, code string, err error) Outcome {
	outcome.Benchmark.Status, outcome.Benchmark.ErrorCode, outcome.Benchmark.Error = model.StatusFailed, code, err.Error()
	return outcome
}

func validateBucket(name, prefix, runID string) error {
	if name != prefix+"-"+runID || len(name) > 63 {
		return errors.New("bucket name does not match the generated run identity")
	}
	return nil
}

func (engine *Engine) Cleanup(ctx context.Context, runID string, resource manifest.Resource) error {
	prefix := resource.Properties["bucket_prefix"]
	if resource.Kind != "s3_bucket" || resource.Marker != markerKey || resource.Name != prefix+"-"+runID {
		return errors.New("cleanup refused: local manifest ownership does not match generated S3 resource")
	}
	object, err := engine.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(resource.Name), Key: aws.String(markerKey)})
	if err != nil {
		return fmt.Errorf("read S3 ownership marker: %w", err)
	}
	var remote marker
	decodeErr := json.NewDecoder(io.LimitReader(object.Body, 16*1024)).Decode(&remote)
	closeErr := object.Body.Close()
	if decodeErr != nil || closeErr != nil || remote.SchemaVersion != manifest.SchemaVersion || remote.RunID != runID {
		return fmt.Errorf("cleanup refused: remote S3 marker mismatch: %w", errors.Join(decodeErr, closeErr))
	}
	continuation := (*string)(nil)
	for {
		listed, err := engine.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(resource.Name), ContinuationToken: continuation})
		if err != nil {
			return fmt.Errorf("list owned S3 bucket: %w", err)
		}
		if len(listed.Contents) > 0 {
			objects := make([]types.ObjectIdentifier, 0, len(listed.Contents))
			for _, item := range listed.Contents {
				objects = append(objects, types.ObjectIdentifier{Key: item.Key})
			}
			if _, err := engine.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: aws.String(resource.Name), Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)}}); err != nil {
				return fmt.Errorf("delete owned S3 objects: %w", err)
			}
		}
		if !aws.ToBool(listed.IsTruncated) {
			break
		}
		continuation = listed.NextContinuationToken
	}
	_, err = engine.client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(resource.Name)})
	if err != nil {
		return fmt.Errorf("delete owned S3 bucket: %w", err)
	}
	return nil
}
