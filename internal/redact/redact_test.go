package redact

import (
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	input := "connect postgresql://bench:hunter2@db.example/test?token=abc explicit-secret"
	output := String(input, "explicit-secret")
	for _, secret := range []string{"hunter2", "abc", "explicit-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("secret %q leaked in %q", secret, output)
		}
	}
}
