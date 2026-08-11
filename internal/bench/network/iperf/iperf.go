package iperf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/LIHA-Research/lihacloud-bench/internal/model"
	"github.com/LIHA-Research/lihacloud-bench/internal/redact"
)

type Detection struct {
	Path    string
	Engine  string
	Version string
}

type Spec struct {
	Profile          string
	Target           string
	Duration         time.Duration
	UDPBitsPerSecond uint64
	Comparable       bool
}

func Detect(ctx context.Context) Detection {
	binary, engine := "iperf3", "iperf3"
	if runtime.GOOS == "windows" {
		binary, engine = "iperf", "iperf2"
	}
	path, _ := exec.LookPath(binary)
	result := Detection{Path: path, Engine: engine}
	if path != "" {
		output, _ := exec.CommandContext(ctx, path, "--version").CombinedOutput()
		line, _, _ := strings.Cut(strings.TrimSpace(string(output)), "\n")
		result.Version = strings.TrimSpace(line)
	}
	return result
}

func DefaultSpec(profile, target string, udpBitsPerSecond uint64) Spec {
	if profile == "quick" {
		return Spec{Profile: profile, Target: target, Duration: 5 * time.Second, UDPBitsPerSecond: udpBitsPerSecond, Comparable: false}
	}
	return Spec{Profile: profile, Target: target, Duration: 30 * time.Second, UDPBitsPerSecond: udpBitsPerSecond, Comparable: true}
}

func Run(ctx context.Context, spec Spec) model.Benchmark {
	detection := Detect(ctx)
	benchmark := model.Benchmark{
		ID: "network-iperf", Category: "network-iperf", Status: model.StatusSuccess,
		Engine: detection.Engine, EngineVersion: detection.Version, WorkloadVersion: "iperf-network-v1", Comparable: spec.Comparable,
		Parameters: map[string]any{"duration_seconds": spec.Duration.Seconds(), "streams": []int{1, 8}, "udp_bits_per_second": spec.UDPBitsPerSecond},
		Metrics:    []model.Metric{}, Warnings: []string{},
	}
	if detection.Path == "" {
		benchmark.Status, benchmark.ErrorCode, benchmark.Error = model.StatusBlocked, "iperf_unavailable", detection.Engine+" is not installed"
		benchmark.Comparable = false
		return benchmark
	}
	if strings.TrimSpace(spec.Target) == "" {
		return fail(benchmark, "iperf_target_missing", errors.New("iperf target is required"), spec.Target)
	}
	if detection.Engine == "iperf2" {
		benchmark.Warnings = append(benchmark.Warnings, "Windows uses the officially supported iperf2 engine; reverse mode uses iperf2 tradeoff mode and is not directly comparable to iperf3")
	}
	for _, streams := range []int{1, 8} {
		forward, err := runTCP(ctx, detection, spec.Target, spec.Duration, streams, false)
		if err != nil {
			return fail(benchmark, "iperf_tcp_forward_failed", err, spec.Target)
		}
		reverse, err := runTCP(ctx, detection, spec.Target, spec.Duration, streams, true)
		if err != nil {
			return fail(benchmark, "iperf_tcp_reverse_failed", err, spec.Target)
		}
		benchmark.Metrics = append(benchmark.Metrics,
			model.Metric{Name: fmt.Sprintf("tcp_upload_%d_stream", streams), Value: forward.BitsPerSecond / 1_000_000, Unit: "Mbit/s", Direction: "higher"},
			model.Metric{Name: fmt.Sprintf("tcp_download_%d_stream", streams), Value: reverse.BitsPerSecond / 1_000_000, Unit: "Mbit/s", Direction: "higher"},
		)
	}
	if spec.UDPBitsPerSecond > 0 {
		udp, err := runUDP(ctx, detection, spec.Target, spec.Duration, spec.UDPBitsPerSecond)
		if err != nil {
			return fail(benchmark, "iperf_udp_failed", err, spec.Target)
		}
		benchmark.Metrics = append(benchmark.Metrics,
			model.Metric{Name: "udp_throughput", Value: udp.BitsPerSecond / 1_000_000, Unit: "Mbit/s", Direction: "higher"},
			model.Metric{Name: "udp_jitter", Value: udp.JitterMS, Unit: "ms", Direction: "lower"},
			model.Metric{Name: "udp_loss", Value: udp.LostPercent / 100, Unit: "ratio", Direction: "lower"},
		)
		if detection.Engine == "iperf2" {
			benchmark.Warnings = append(benchmark.Warnings, "iperf2 UDP is measured in the forward direction only")
		}
	}
	return benchmark
}

type Parsed struct {
	BitsPerSecond float64
	JitterMS      float64
	LostPercent   float64
	Retransmits   uint64
}

func runTCP(ctx context.Context, detection Detection, target string, duration time.Duration, streams int, reverse bool) (Parsed, error) {
	arguments := append(targetArguments(target), "-t", strconv.Itoa(max(1, int(duration.Seconds()))), "-P", strconv.Itoa(streams))
	if detection.Engine == "iperf3" {
		arguments = append(arguments, "-J")
		if reverse {
			arguments = append(arguments, "-R")
		}
	} else {
		arguments = append(arguments, "-f", "b")
		if reverse {
			arguments = append(arguments, "-r")
		}
	}
	output, err := exec.CommandContext(ctx, detection.Path, arguments...).CombinedOutput()
	if err != nil {
		return Parsed{}, commandError(err, output)
	}
	if detection.Engine == "iperf3" {
		return ParseIPerf3(output, false)
	}
	return ParseIPerf2(output, false)
}

func runUDP(ctx context.Context, detection Detection, target string, duration time.Duration, bitsPerSecond uint64) (Parsed, error) {
	arguments := append(targetArguments(target), "-u", "-b", strconv.FormatUint(bitsPerSecond, 10), "-t", strconv.Itoa(max(1, int(duration.Seconds()))))
	if detection.Engine == "iperf3" {
		arguments = append(arguments, "-J")
	} else {
		arguments = append(arguments, "-f", "b")
	}
	output, err := exec.CommandContext(ctx, detection.Path, arguments...).CombinedOutput()
	if err != nil {
		return Parsed{}, commandError(err, output)
	}
	if detection.Engine == "iperf3" {
		return ParseIPerf3(output, true)
	}
	return ParseIPerf2(output, true)
}

func targetArguments(target string) []string {
	host, port, err := net.SplitHostPort(target)
	if err == nil {
		return []string{"-c", host, "-p", port}
	}
	return []string{"-c", target}
}

func ParseIPerf3(output []byte, udp bool) (Parsed, error) {
	var value struct {
		Error string `json:"error"`
		End   struct {
			SumReceived struct {
				BitsPerSecond float64 `json:"bits_per_second"`
			} `json:"sum_received"`
			SumSent struct {
				BitsPerSecond float64 `json:"bits_per_second"`
				Retransmits   uint64  `json:"retransmits"`
			} `json:"sum_sent"`
			Sum struct {
				BitsPerSecond float64 `json:"bits_per_second"`
				JitterMS      float64 `json:"jitter_ms"`
				LostPercent   float64 `json:"lost_percent"`
			} `json:"sum"`
		} `json:"end"`
	}
	if err := json.Unmarshal(output, &value); err != nil {
		return Parsed{}, fmt.Errorf("parse iperf3 JSON: %w", err)
	}
	if value.Error != "" {
		return Parsed{}, errors.New(value.Error)
	}
	if udp {
		if value.End.Sum.BitsPerSecond <= 0 {
			return Parsed{}, errors.New("iperf3 JSON is missing UDP summary")
		}
		return Parsed{BitsPerSecond: value.End.Sum.BitsPerSecond, JitterMS: value.End.Sum.JitterMS, LostPercent: value.End.Sum.LostPercent}, nil
	}
	bits := value.End.SumReceived.BitsPerSecond
	if bits <= 0 {
		bits = value.End.SumSent.BitsPerSecond
	}
	if bits <= 0 {
		return Parsed{}, errors.New("iperf3 JSON is missing TCP summary")
	}
	return Parsed{BitsPerSecond: bits, Retransmits: value.End.SumSent.Retransmits}, nil
}

var (
	bitsPattern = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s+bits/sec`)
	udpPattern  = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s+ms\s+\d+/\s*\d+\s+\(([0-9]+(?:\.[0-9]+)?)%\)`)
)

func ParseIPerf2(output []byte, udp bool) (Parsed, error) {
	text := string(output)
	bitsMatches := bitsPattern.FindAllStringSubmatch(text, -1)
	if len(bitsMatches) == 0 {
		return Parsed{}, errors.New("iperf2 output is missing throughput")
	}
	bits, err := strconv.ParseFloat(bitsMatches[len(bitsMatches)-1][1], 64)
	if err != nil {
		return Parsed{}, err
	}
	result := Parsed{BitsPerSecond: bits}
	if udp {
		matches := udpPattern.FindAllStringSubmatch(text, -1)
		if len(matches) == 0 {
			return Parsed{}, errors.New("iperf2 output is missing UDP jitter or loss")
		}
		last := matches[len(matches)-1]
		result.JitterMS, _ = strconv.ParseFloat(last[1], 64)
		result.LostPercent, _ = strconv.ParseFloat(last[2], 64)
	}
	return result, nil
}

func commandError(err error, output []byte) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, redact.String(message))
}

func fail(benchmark model.Benchmark, code string, err error, secrets ...string) model.Benchmark {
	benchmark.Status, benchmark.ErrorCode, benchmark.Error = model.StatusFailed, code, redact.String(err.Error(), secrets...)
	return benchmark
}
