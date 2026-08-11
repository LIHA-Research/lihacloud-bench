package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(nil, bytes.NewReader(nil), &stdout, &stderr); code != 0 {
		t.Fatalf("Run() code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("help output missing usage: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"unknown"}, bytes.NewReader(nil), &stdout, &stderr); code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestConfirmation(t *testing.T) {
	var output bytes.Buffer
	if err := confirmation(strings.NewReader("yes\n"), &output, false, true); err != nil {
		t.Fatal(err)
	}
	if err := confirmation(strings.NewReader("yes\n"), &output, false, false); err == nil {
		t.Fatal("non-interactive confirmation was accepted without --yes")
	}
	if err := confirmation(strings.NewReader(""), &output, true, false); err != nil {
		t.Fatal(err)
	}
}

func TestPlanQuick(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"plan", "--profile", "quick", "--only", "cpu"}, bytes.NewReader(nil), &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "builtin-sha256") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestCLIProfileOverridesInvalidEnvironmentValue(t *testing.T) {
	t.Setenv("LIHACLOUD_BENCH_PROFILE", "invalid")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"plan", "--profile", "quick", "--only", "cpu"}, bytes.NewReader(nil), &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "builtin-sha256") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestCLIKeepResourcesOverridesInvalidEnvironmentValue(t *testing.T) {
	t.Setenv("LIHACLOUD_BENCH_KEEP_RESOURCES", "not-a-boolean")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"plan", "--profile", "quick", "--only", "cpu", "--keep-resources"}, bytes.NewReader(nil), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestPeerServeRequiresToken(t *testing.T) {
	t.Setenv("LIHACLOUD_BENCH_PEER_TOKEN", "")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"network", "peer", "serve", "--listen", "127.0.0.1:0"}, bytes.NewReader(nil), &stdout, &stderr); code != 2 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
