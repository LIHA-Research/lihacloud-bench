package clickhouse

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestClickCannonConnection(t *testing.T) {
	connection, err := clickCannonConnection("clickhouse://bench:secret@example.com:9000/default?secure=true", "owned")
	if err != nil {
		t.Fatal(err)
	}
	if connection.Address != "example.com:9000" || !connection.Secure || connection.User != "bench" || connection.Password != "secret" || !strings.Contains(connection.DSN, "/owned") {
		t.Fatalf("unexpected connection: %+v", connection)
	}
}

func TestClickCannonRejectsHTTP(t *testing.T) {
	if _, err := clickCannonConnection("https://example.com:8443/default", "owned"); err == nil {
		t.Fatal("HTTP URL accepted for native helper")
	}
}

func TestFetchExpectedChecksum(t *testing.T) {
	// Parsing is covered through the same line format used by GitHub checksums;
	// avoid a network dependency by exercising the missing-release path.
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	if _, err := fetchExpectedChecksum(ctx, "https://example.invalid/checksums", "asset"); err == nil {
		t.Fatal("expected canceled fetch")
	}
}
