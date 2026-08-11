package ndt7bench

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/LIHA-Research/lihacloud-bench/internal/model"
	ndt7 "github.com/m-lab/ndt7-client-go"
	"github.com/m-lab/ndt7-client-go/spec"
)

const Version = "v0.10.1"

type Spec struct {
	Profile            string
	ToolVersion        string
	DataPolicyAccepted bool
	Comparable         bool
}

func DefaultSpec(profile, toolVersion string, accepted bool) Spec {
	return Spec{Profile: profile, ToolVersion: toolVersion, DataPolicyAccepted: accepted, Comparable: profile != "quick"}
}

func Run(ctx context.Context, runSpec Spec) model.Benchmark {
	benchmark := model.Benchmark{
		ID: "network-internet", Category: "network-internet", Status: model.StatusSuccess,
		Engine: "mlab-ndt7", EngineVersion: Version, WorkloadVersion: "ndt7-protocol-v7", Comparable: runSpec.Comparable,
		Parameters: map[string]any{"data_policy_accepted": runSpec.DataPolicyAccepted}, Metrics: []model.Metric{}, Warnings: []string{},
	}
	if !runSpec.DataPolicyAccepted {
		benchmark.Status, benchmark.ErrorCode, benchmark.Error, benchmark.Comparable = model.StatusBlocked, "mlab_policy_not_accepted", "M-Lab data policy consent is required", false
		return benchmark
	}
	version := runSpec.ToolVersion
	if version == "" {
		version = "dev"
	}
	client := ndt7.NewClient("lihacloud-bench", version)
	downloadContext, cancelDownload := context.WithTimeout(ctx, 60*time.Second)
	download, err := client.StartDownload(downloadContext)
	if err != nil {
		cancelDownload()
		return fail(benchmark, "ndt7_download_failed", "NDT7 download could not connect")
	}
	for range download {
	}
	cancelDownload()
	uploadContext, cancelUpload := context.WithTimeout(ctx, 60*time.Second)
	upload, err := client.StartUpload(uploadContext)
	if err != nil {
		cancelUpload()
		return fail(benchmark, "ndt7_upload_failed", "NDT7 upload could not connect")
	}
	for range upload {
	}
	cancelUpload()
	metrics, err := summarize(client.Results())
	if err != nil {
		return fail(benchmark, "ndt7_metrics_missing", err.Error())
	}
	benchmark.Metrics = metrics
	return benchmark
}

func summarize(results map[spec.TestKind]*ndt7.LatestMeasurements) ([]model.Metric, error) {
	download, downloadOK := results[spec.TestDownload]
	upload, uploadOK := results[spec.TestUpload]
	if !downloadOK || !uploadOK || download.Client.AppInfo == nil || download.Client.AppInfo.ElapsedTime <= 0 || upload.Server.TCPInfo == nil || upload.Server.TCPInfo.ElapsedTime <= 0 {
		return nil, errors.New("NDT7 did not return complete application and TCP measurements")
	}
	downloadSeconds := float64(download.Client.AppInfo.ElapsedTime) / 1e6
	uploadSeconds := float64(upload.Server.TCPInfo.ElapsedTime) / 1e6
	downloadThroughput := float64(download.Client.AppInfo.NumBytes*8) / downloadSeconds / 1_000_000
	uploadThroughput := float64(upload.Server.TCPInfo.BytesReceived*8) / uploadSeconds / 1_000_000
	metrics := []model.Metric{
		{Name: "download_throughput", Value: downloadThroughput, Unit: "Mbit/s", Direction: "higher"},
		{Name: "upload_throughput", Value: uploadThroughput, Unit: "Mbit/s", Direction: "higher"},
		{Name: "upload_min_rtt", Value: float64(upload.Server.TCPInfo.MinRTT) / 1000, Unit: "ms", Direction: "lower"},
	}
	if download.Server.TCPInfo != nil {
		metrics = append(metrics, model.Metric{Name: "download_min_rtt", Value: float64(download.Server.TCPInfo.MinRTT) / 1000, Unit: "ms", Direction: "lower"})
		if download.Server.TCPInfo.BytesSent > 0 {
			metrics = append(metrics, model.Metric{Name: "download_retransmission", Value: float64(download.Server.TCPInfo.BytesRetrans) / float64(download.Server.TCPInfo.BytesSent), Unit: "ratio", Direction: "lower"})
		}
	}
	return metrics, nil
}

func fail(benchmark model.Benchmark, code, message string) model.Benchmark {
	benchmark.Status, benchmark.ErrorCode, benchmark.Error = model.StatusFailed, code, fmt.Sprintf("%s", message)
	return benchmark
}
