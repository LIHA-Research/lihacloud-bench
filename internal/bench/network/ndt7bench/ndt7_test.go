package ndt7bench

import (
	"testing"

	ndt7 "github.com/m-lab/ndt7-client-go"
	"github.com/m-lab/ndt7-client-go/spec"
)

func TestSummarize(t *testing.T) {
	downloadApp := &spec.AppInfo{}
	downloadApp.ElapsedTime, downloadApp.NumBytes = 2_000_000, 25_000_000
	downloadTCP := &spec.TCPInfo{}
	downloadTCP.MinRTT, downloadTCP.BytesSent, downloadTCP.BytesRetrans = 2_000, 1000, 10
	uploadTCP := &spec.TCPInfo{}
	uploadTCP.ElapsedTime, uploadTCP.BytesReceived, uploadTCP.MinRTT = 4_000_000, 50_000_000, 3_000
	downloadClient := spec.Measurement{AppInfo: downloadApp}
	downloadServer := spec.Measurement{TCPInfo: downloadTCP}
	uploadServer := spec.Measurement{TCPInfo: uploadTCP}
	metrics, err := summarize(map[spec.TestKind]*ndt7.LatestMeasurements{
		spec.TestDownload: {Client: downloadClient, Server: downloadServer},
		spec.TestUpload:   {Server: uploadServer},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 5 || metrics[0].Value != 100 || metrics[1].Value != 100 || metrics[4].Value != .01 {
		t.Fatalf("metrics=%+v", metrics)
	}
}

func TestPolicyRequired(t *testing.T) {
	result := Run(t.Context(), DefaultSpec("quick", "dev", false))
	if result.Status != "blocked" || result.ErrorCode != "mlab_policy_not_accepted" {
		t.Fatalf("result=%+v", result)
	}
}
