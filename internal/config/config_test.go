package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsValidate(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("Defaults().Validate() error = %v", err)
	}
}

func TestLoadRejectsUnknownAndSecretFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := strings.Replace(Example, "version: 1", "version: 1\npostgres_url: postgres://secret", 1)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "postgres_url") {
		t.Fatalf("Load() error = %v, want unknown field rejection", err)
	}
}

func TestLoadExample(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(Example), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profile != "standard" || cfg.S3.Region != "us-east-1" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestEnvironmentPrecedence(t *testing.T) {
	cfg := Defaults()
	cfg.Profile = "standard"
	cfg.Network.IPerf.Enabled = true
	cfg.Network.Internet.Enabled = true
	err := ApplyEnvironment(&cfg, func(name string) (string, bool) {
		values := map[string]string{"LIHACLOUD_BENCH_PROFILE": "quick", "LIHACLOUD_BENCH_KEEP_RESOURCES": "true", "LIHACLOUD_BENCH_IPERF_TARGET": "peer.example:5201", "LIHACLOUD_BENCH_ACCEPT_MLAB_DATA_POLICY": "true"}
		value, ok := values[name]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profile != "quick" || !cfg.KeepResources || cfg.Network.IPerf.Target != "peer.example:5201" || !cfg.Network.Internet.AcceptMLabDataPolicy {
		t.Fatalf("environment was not applied: %+v", cfg)
	}
}

func TestLoadAllowsEnvironmentCompletedNetworkConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := strings.Replace(Example, "  iperf:\n    enabled: false", "  iperf:\n    enabled: true", 1)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyEnvironment(&cfg, func(name string) (string, bool) {
		if name == "LIHACLOUD_BENCH_IPERF_TARGET" {
			return "peer.example", true
		}
		return "", false
	}); err != nil {
		t.Fatal(err)
	}
}
