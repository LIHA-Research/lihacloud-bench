package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

const DefaultPath = "lihacloud-bench.yaml"

type Config struct {
	Version       int              `yaml:"version"`
	Profile       string           `yaml:"profile"`
	HostLabel     string           `yaml:"host_label"`
	Output        string           `yaml:"output"`
	KeepResources bool             `yaml:"keep_resources"`
	Disk          DiskConfig       `yaml:"disk"`
	S3            S3Config         `yaml:"s3"`
	Postgres      DatabaseConfig   `yaml:"postgres"`
	ClickHouse    ClickHouseConfig `yaml:"clickhouse"`
	Network       NetworkConfig    `yaml:"network"`
}

// ApplyEnvironment overlays non-secret environment configuration. Credential
// values remain in their native providers and are never copied into Config.
func ApplyEnvironment(cfg *Config, lookup func(string) (string, bool)) error {
	stringsMap := []struct {
		name   string
		target *string
	}{
		{"LIHACLOUD_BENCH_PROFILE", &cfg.Profile},
		{"LIHACLOUD_BENCH_OUTPUT", &cfg.Output},
		{"LIHACLOUD_BENCH_HOST_LABEL", &cfg.HostLabel},
		{"LIHACLOUD_BENCH_DISK_PATH", &cfg.Disk.Path},
		{"LIHACLOUD_BENCH_S3_ENDPOINT", &cfg.S3.Endpoint},
		{"LIHACLOUD_BENCH_S3_REGION", &cfg.S3.Region},
	}
	for _, item := range stringsMap {
		if value, ok := lookup(item.name); ok {
			*item.target = value
		}
	}
	booleans := []struct {
		name   string
		target *bool
	}{
		{"LIHACLOUD_BENCH_KEEP_RESOURCES", &cfg.KeepResources},
		{"LIHACLOUD_BENCH_S3_ENABLED", &cfg.S3.Enabled},
		{"LIHACLOUD_BENCH_S3_PATH_STYLE", &cfg.S3.PathStyle},
	}
	for _, item := range booleans {
		if value, ok := lookup(item.name); ok {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("%s must be a boolean: %w", item.name, err)
			}
			*item.target = parsed
		}
	}
	return cfg.Validate()
}

type DiskConfig struct {
	Path string `yaml:"path"`
}

type S3Config struct {
	Enabled      bool   `yaml:"enabled"`
	Endpoint     string `yaml:"endpoint"`
	Region       string `yaml:"region"`
	PathStyle    bool   `yaml:"path_style"`
	BucketPrefix string `yaml:"bucket_prefix"`
}

type DatabaseConfig struct {
	Enabled bool   `yaml:"enabled"`
	URLEnv  string `yaml:"url_env"`
}

type ClickHouseConfig struct {
	Enabled             bool   `yaml:"enabled"`
	URLEnv              string `yaml:"url_env"`
	ClickBenchCacheMode string `yaml:"clickbench_cache_mode"`
}

type NetworkConfig struct {
	Peer     PeerConfig     `yaml:"peer"`
	IPerf    IPerfConfig    `yaml:"iperf"`
	Internet InternetConfig `yaml:"internet"`
}

type PeerConfig struct {
	Enabled                bool   `yaml:"enabled"`
	Target                 string `yaml:"target"`
	CertificateFingerprint string `yaml:"certificate_fingerprint"`
	TokenEnv               string `yaml:"token_env"`
}

type IPerfConfig struct {
	Enabled          bool   `yaml:"enabled"`
	Target           string `yaml:"target"`
	UDPBitsPerSecond uint64 `yaml:"udp_bits_per_second"`
}

type InternetConfig struct {
	Enabled              bool `yaml:"enabled"`
	AcceptMLabDataPolicy bool `yaml:"accept_mlab_data_policy"`
}

func Defaults() Config {
	return Config{
		Version:    1,
		Profile:    "standard",
		Output:     "results",
		S3:         S3Config{Region: "us-east-1", BucketPrefix: "lihacloud-bench"},
		Postgres:   DatabaseConfig{URLEnv: "LIHACLOUD_BENCH_POSTGRES_URL"},
		ClickHouse: ClickHouseConfig{URLEnv: "LIHACLOUD_BENCH_CLICKHOUSE_URL", ClickBenchCacheMode: "lukewarm"},
		Network: NetworkConfig{
			Peer: PeerConfig{TokenEnv: "LIHACLOUD_BENCH_PEER_TOKEN"},
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Defaults()
	if path == "" {
		if _, err := os.Stat(DefaultPath); err == nil {
			path = DefaultPath
		} else if !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("stat default config: %w", err)
		}
	}
	if path == "" {
		return cfg, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Config{}, errors.New("config must contain one YAML document")
		}
		return Config{}, fmt.Errorf("decode trailing config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported config version %d", c.Version)
	}
	if c.Profile != "standard" && c.Profile != "quick" {
		return fmt.Errorf("profile must be standard or quick, got %q", c.Profile)
	}
	if strings.TrimSpace(c.Output) == "" {
		return errors.New("output must not be empty")
	}
	if c.S3.Region == "" || c.S3.BucketPrefix == "" {
		return errors.New("s3 region and bucket_prefix must not be empty")
	}
	if len(c.S3.BucketPrefix)+1+36 > 63 {
		return errors.New("s3 bucket_prefix is too long for a UUID-suffixed bucket")
	}
	if c.Postgres.Enabled && c.Postgres.URLEnv == "" {
		return errors.New("postgres url_env must not be empty when enabled")
	}
	if c.ClickHouse.Enabled && c.ClickHouse.URLEnv == "" {
		return errors.New("clickhouse url_env must not be empty when enabled")
	}
	if c.ClickHouse.ClickBenchCacheMode != "lukewarm" && c.ClickHouse.ClickBenchCacheMode != "true-cold" {
		return errors.New("clickhouse clickbench_cache_mode must be lukewarm or true-cold")
	}
	if c.Network.Internet.Enabled && !c.Network.Internet.AcceptMLabDataPolicy {
		return errors.New("network.internet requires accept_mlab_data_policy: true")
	}
	return nil
}

const Example = `version: 1
profile: standard
host_label: ""
output: results
keep_resources: false

disk:
  path: ""

s3:
  enabled: false
  endpoint: ""
  region: us-east-1
  path_style: false
  bucket_prefix: lihacloud-bench

postgres:
  enabled: false
  url_env: LIHACLOUD_BENCH_POSTGRES_URL

clickhouse:
  enabled: false
  url_env: LIHACLOUD_BENCH_CLICKHOUSE_URL
  clickbench_cache_mode: lukewarm

network:
  peer:
    enabled: false
    target: ""
    certificate_fingerprint: ""
    token_env: LIHACLOUD_BENCH_PEER_TOKEN
  iperf:
    enabled: false
    target: ""
    udp_bits_per_second: 0
  internet:
    enabled: false
    accept_mlab_data_policy: false
`
