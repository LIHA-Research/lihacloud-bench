package peer

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestPeerLoopback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	serverErrors := make(chan error, 1)
	server := &Server{Listen: "127.0.0.1:0", Token: "one-time-test-token"}
	go func() {
		serverErrors <- server.Serve(ctx, writer)
	}()
	scanner := bufio.NewScanner(reader)
	if !scanner.Scan() {
		t.Fatal("server did not emit listen address")
	}
	target := strings.TrimPrefix(scanner.Text(), "Peer server listening on ")
	if !scanner.Scan() {
		t.Fatal("server did not emit certificate fingerprint")
	}
	fingerprint := strings.TrimPrefix(scanner.Text(), "Certificate fingerprint: ")
	result := Run(context.Background(), Spec{Profile: "quick", Target: target, Fingerprint: fingerprint, Token: "one-time-test-token", Duration: time.Second, RTTSamples: 2, Comparable: false})
	if result.Status != "success" || len(result.Metrics) != 8 {
		t.Fatalf("unexpected result: %+v", result)
	}
	for _, metric := range result.Metrics[4:] {
		if metric.Value <= 0 {
			t.Fatalf("non-positive throughput: %+v", metric)
		}
	}
	second := Run(context.Background(), Spec{Profile: "quick", Target: target, Fingerprint: fingerprint, Token: "one-time-test-token", Duration: time.Second, RTTSamples: 1, Comparable: false})
	if second.Status != "failed" || second.ErrorCode != "peer_rtt_failed" {
		t.Fatalf("one-time token was reused: %+v", second)
	}
	cancel()
	if err := <-serverErrors; err != nil {
		t.Fatalf("server shutdown: %v", err)
	}
}

func TestPeerRejectsWrongFingerprint(t *testing.T) {
	result := Run(context.Background(), Spec{Target: "127.0.0.1:1", Fingerprint: "bad", Token: "token", Duration: time.Second, RTTSamples: 1})
	if result.Status != "failed" || result.ErrorCode != "peer_configuration" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestFingerprintFormat(t *testing.T) {
	_, fingerprint, err := ephemeralCertificate()
	if err != nil {
		t.Fatal(err)
	}
	if len(normalizeFingerprint(fingerprint)) != 64 {
		t.Fatalf("fingerprint=%q", fingerprint)
	}
}
