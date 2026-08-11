package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/LIHA-Research/lihacloud-bench/internal/bench/disk"
	"github.com/LIHA-Research/lihacloud-bench/internal/bench/network/iperf"
	"github.com/LIHA-Research/lihacloud-bench/internal/bench/network/peer"
	"github.com/LIHA-Research/lihacloud-bench/internal/buildinfo"
	cacheio "github.com/LIHA-Research/lihacloud-bench/internal/cache"
	"github.com/LIHA-Research/lihacloud-bench/internal/config"
	"github.com/LIHA-Research/lihacloud-bench/internal/model"
	resultio "github.com/LIHA-Research/lihacloud-bench/internal/result"
	"github.com/LIHA-Research/lihacloud-bench/internal/runner"
	"github.com/google/uuid"
	"golang.org/x/term"
)

const usage = `lihacloud-bench measures compute, storage, database, and network performance.

Usage:
  lihacloud-bench init [flags]
  lihacloud-bench doctor [flags]
  lihacloud-bench plan [flags]
  lihacloud-bench run [flags]
  lihacloud-bench cpu|memory|disk [flags]
  lihacloud-bench object s3 [flags]
  lihacloud-bench postgres pgbench [flags]
  lihacloud-bench clickhouse clickbench|clickcannon [flags]
  lihacloud-bench network peer serve|run [flags]
  lihacloud-bench network iperf|internet [flags]
  lihacloud-bench cleanup --run-id <uuid>
  lihacloud-bench cache status|prune

Common flags:
  --config <path>          Configuration YAML (default lihacloud-bench.yaml)
  --profile <name>        standard or quick
  --only <categories>     Comma-separated benchmark categories
  --output <directory>    JSON result directory
  --yes                   Approve a non-interactive run
  --keep-resources        Preserve run-owned remote resources
  -h, --help              Show help
  -v, --version           Show version
`

type commonOptions struct {
	configPath string
	profile    string
	output     string
	only       stringList
	yes        bool
	keep       bool
	profileSet bool
	outputSet  bool
	keepSet    bool
}

// Run executes the command line and returns a process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || slices.Contains([]string{"help", "-h", "--help"}, args[0]) {
		_, _ = io.WriteString(stdout, usage)
		return 0
	}
	if slices.Contains([]string{"-v", "--version", "version"}, args[0]) {
		_, _ = fmt.Fprintf(stdout, "lihacloud-bench %s (%s, %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return 0
	}
	switch args[0] {
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "plan":
		return runPlan(args[1:], nil, stdout, stderr)
	case "run":
		return runBenchmarks(args[1:], nil, stdin, stdout, stderr)
	case "cpu", "memory", "disk":
		return runBenchmarks(args[1:], []string{args[0]}, stdin, stdout, stderr)
	case "object":
		if len(args) > 1 && args[1] == "s3" {
			return runBenchmarks(args[2:], []string{"object-s3"}, stdin, stdout, stderr)
		}
	case "postgres":
		if len(args) > 1 && args[1] == "pgbench" {
			return runBenchmarks(args[2:], []string{"postgres-pgbench"}, stdin, stdout, stderr)
		}
	case "clickhouse":
		if len(args) > 1 && args[1] == "clickbench" {
			return runBenchmarks(args[2:], []string{"clickhouse-clickbench"}, stdin, stdout, stderr)
		}
		if len(args) > 1 && args[1] == "clickcannon" {
			return runBenchmarks(args[2:], []string{"clickhouse-clickcannon"}, stdin, stdout, stderr)
		}
	case "network":
		if len(args) > 1 && args[1] == "iperf" {
			return runBenchmarks(args[2:], []string{"network-iperf"}, stdin, stdout, stderr)
		}
		if len(args) > 1 && args[1] == "internet" {
			return runBenchmarks(args[2:], []string{"network-internet"}, stdin, stdout, stderr)
		}
		if len(args) > 2 && args[1] == "peer" && args[2] == "run" {
			return runBenchmarks(args[3:], []string{"network-peer"}, stdin, stdout, stderr)
		}
		if len(args) > 2 && args[1] == "peer" && args[2] == "serve" {
			return runPeerServe(args[3:], stdout, stderr)
		}
	case "cleanup":
		return runCleanup(args[1:], stdout, stderr)
	case "cache":
		return runCache(args[1:], stdout, stderr)
	}
	_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n%s", strings.Join(args, " "), usage)
	return 2
}

func common(args []string, stderr io.Writer) (commonOptions, config.Config, error) {
	options := commonOptions{}
	set := flag.NewFlagSet("benchmark", flag.ContinueOnError)
	set.SetOutput(stderr)
	set.StringVar(&options.configPath, "config", "", "configuration YAML")
	set.StringVar(&options.profile, "profile", "", "standard or quick")
	set.StringVar(&options.output, "output", "", "result directory")
	set.Var(&options.only, "only", "benchmark category")
	set.BoolVar(&options.yes, "yes", false, "approve run")
	set.BoolVar(&options.keep, "keep-resources", false, "preserve resources")
	if err := set.Parse(args); err != nil {
		return options, config.Config{}, err
	}
	if set.NArg() != 0 {
		return options, config.Config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(set.Args(), " "))
	}
	set.Visit(func(item *flag.Flag) {
		switch item.Name {
		case "profile":
			options.profileSet = true
		case "output":
			options.outputSet = true
		case "keep-resources":
			options.keepSet = true
		}
	})
	cfg, err := config.Load(options.configPath)
	if err != nil {
		return options, config.Config{}, err
	}
	lookupEnvironment := func(name string) (string, bool) {
		if (name == "LIHACLOUD_BENCH_PROFILE" && options.profileSet) ||
			(name == "LIHACLOUD_BENCH_OUTPUT" && options.outputSet) ||
			(name == "LIHACLOUD_BENCH_KEEP_RESOURCES" && options.keepSet) {
			return "", false
		}
		return os.LookupEnv(name)
	}
	if err := config.ApplyEnvironment(&cfg, lookupEnvironment); err != nil {
		return options, config.Config{}, err
	}
	if options.profileSet {
		cfg.Profile = options.profile
	}
	if options.outputSet {
		cfg.Output = options.output
	}
	if options.keepSet {
		cfg.KeepResources = options.keep
	}
	if err := cfg.ValidateResolved(); err != nil {
		return options, config.Config{}, err
	}
	return options, cfg, nil
}

func runInit(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("init", flag.ContinueOnError)
	set.SetOutput(stderr)
	path := set.String("config", config.DefaultPath, "configuration path")
	if err := set.Parse(args); err != nil {
		return 2
	}
	if set.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "init accepts no positional arguments")
		return 2
	}
	file, err := os.OpenFile(*path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "initialize config: %v\n", err)
		return 2
	}
	_, writeErr := io.WriteString(file, config.Example)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_, _ = fmt.Fprintf(stderr, "initialize config: %v\n", err)
		return 2
	}
	_, _ = fmt.Fprintf(stdout, "Created %s (credentials stay in environment variables or the AWS credential chain).\n", *path)
	return 0
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	_, cfg, err := common(args, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "doctor: %v\n", err)
		return 2
	}
	failed := false
	_, _ = fmt.Fprintf(stdout, "%-16s %-10s %s\n", "CHECK", "STATUS", "DETAIL")
	_, _ = fmt.Fprintf(stdout, "%-16s %-10s %s/%s\n", "host", "OK", runtime.GOOS, runtime.GOARCH)
	detection := disk.Detect(context.Background())
	if detection.Path == "" {
		_, _ = fmt.Fprintf(stdout, "%-16s %-10s %s\n", "fio", "FALLBACK", "not installed; builtin buffered I/O will be used")
	} else {
		_, _ = fmt.Fprintf(stdout, "%-16s %-10s %s (%s)\n", "fio", "OK", detection.Path, detection.Version)
	}
	for _, item := range []struct {
		name, binary string
		enabled      bool
	}{{"pgbench", "pgbench", cfg.Postgres.Enabled}} {
		path, lookupErr := exec.LookPath(item.binary)
		status, detail := "OK", path
		if lookupErr != nil {
			status, detail = "MISSING", "not installed"
			if item.enabled {
				failed = true
			}
		}
		_, _ = fmt.Fprintf(stdout, "%-16s %-10s %s\n", item.name, status, detail)
	}
	iperfDetection := iperf.Detect(context.Background())
	iperfStatus, iperfDetail := "OK", strings.TrimSpace(iperfDetection.Path+" "+iperfDetection.Version)
	if iperfDetection.Path == "" {
		iperfStatus, iperfDetail = "MISSING", iperfDetection.Engine+" is not installed"
		if cfg.Network.IPerf.Enabled {
			failed = true
		}
	}
	_, _ = fmt.Fprintf(stdout, "%-16s %-10s %s\n", "iperf", iperfStatus, iperfDetail)
	for _, item := range []struct {
		name, env string
		enabled   bool
	}{{"postgres-url", cfg.Postgres.URLEnv, cfg.Postgres.Enabled}, {"clickhouse-url", cfg.ClickHouse.URLEnv, cfg.ClickHouse.Enabled}, {"peer-token", cfg.Network.Peer.TokenEnv, cfg.Network.Peer.Enabled}} {
		_, present := os.LookupEnv(item.env)
		status := "NOT SET"
		if present {
			status = "SET (redacted)"
		}
		if item.enabled && !present {
			failed = true
		}
		_, _ = fmt.Fprintf(stdout, "%-16s %-10s %s\n", item.name, map[bool]string{true: "OK", false: "INFO"}[present], status)
	}
	if failed {
		return 1
	}
	return 0
}

func runPlan(args, forced []string, stdout, stderr io.Writer) int {
	options, cfg, err := common(args, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "plan: %v\n", err)
		return 2
	}
	categories, err := selected(cfg, options, forced)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "plan: %v\n", err)
		return 2
	}
	plan, err := runner.Plan(context.Background(), cfg, categories, uuid.NewString())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "plan: %v\n", err)
		return 2
	}
	printPlan(stdout, plan)
	return 0
}

func runBenchmarks(args, forced []string, stdin io.Reader, stdout, stderr io.Writer) int {
	options, cfg, err := common(args, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "run: %v\n", err)
		return 2
	}
	categories, err := selected(cfg, options, forced)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "run: %v\n", err)
		return 2
	}
	runID := uuid.NewString()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	plan, err := runner.Plan(ctx, cfg, categories, runID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "run: %v\n", err)
		return 2
	}
	printPlan(stdout, plan)
	if err := confirmation(stdin, stdout, options.yes, interactive(stdin)); err != nil {
		_, _ = fmt.Fprintf(stderr, "run: %v\n", err)
		return 2
	}
	execution, err := runner.Execute(ctx, cfg, categories, runID, model.ToolInfo{Version: buildinfo.Version, Commit: buildinfo.Commit, BuildDate: buildinfo.Date})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "run: %v\n", err)
		return 2
	}
	resultio.PrintSummary(stdout, execution.Result, execution.Path)
	if execution.Result.Run.Status == "interrupted" {
		return 130
	}
	if execution.Result.Run.Status != "success" {
		return 1
	}
	return 0
}

func selected(cfg config.Config, options commonOptions, forced []string) ([]string, error) {
	if len(options.only) > 0 {
		return runner.SelectCategories(cfg, options.only)
	}
	if len(forced) > 0 {
		return forced, nil
	}
	return runner.SelectCategories(cfg, nil)
}

func printPlan(writer io.Writer, plan model.Plan) {
	_, _ = fmt.Fprintf(writer, "Plan\n  Categories: %s\n  Estimated transfer: %.2f MiB\n  Estimated requests: %d\n  Estimated duration: %s\n  External data sharing: %t\n", strings.Join(plan.Categories, ", "), float64(plan.EstimatedTransferBytes)/(1024*1024), plan.EstimatedRequests, (time.Duration(plan.EstimatedDurationSeconds) * time.Second).String(), plan.ExternalDataSharing)
	for _, resource := range plan.Resources {
		_, _ = fmt.Fprintf(writer, "  Resource: %s %s\n", resource.Kind, resource.Name)
	}
	for _, engine := range plan.Engines {
		_, _ = fmt.Fprintf(writer, "  Engine: %s = %s %s [%s]\n", engine.Category, engine.Name, engine.Version, engine.State)
	}
}

func confirmation(reader io.Reader, writer io.Writer, yes, isInteractive bool) error {
	if yes {
		return nil
	}
	if !isInteractive {
		return errors.New("non-interactive execution requires --yes")
	}
	_, _ = io.WriteString(writer, "Proceed? [y/N] ")
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
		return errors.New("run cancelled")
	}
	return nil
}

func interactive(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	for _, item := range strings.Split(value, ",") {
		if strings.TrimSpace(item) != "" {
			*values = append(*values, strings.TrimSpace(item))
		}
	}
	return nil
}

func runCleanup(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("cleanup", flag.ContinueOnError)
	set.SetOutput(stderr)
	runID := set.String("run-id", "", "run UUID")
	if err := set.Parse(args); err != nil {
		return 2
	}
	if _, err := uuid.Parse(*runID); err != nil {
		_, _ = fmt.Fprintln(stderr, "cleanup requires a valid --run-id UUID")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	resources, err := runner.Cleanup(ctx, *runID)
	for _, resource := range resources {
		_, _ = fmt.Fprintf(stdout, "%s %s: %s\n", resource.Kind, resource.Name, resource.CleanupStatus)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cleanup: %v\n", err)
		return 1
	}
	return 0
}

func runCache(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, "cache requires status or prune")
		return 2
	}
	switch args[0] {
	case "status":
		path, files, bytes, err := cacheio.Status()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "cache status: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "%s: %d files, %d bytes\n", path, files, bytes)
	case "prune":
		path, err := cacheio.Prune()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "cache prune: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "Pruned benchmark dataset and helper cache at %s\n", path)
	default:
		_, _ = fmt.Fprintln(stderr, "cache requires status or prune")
		return 2
	}
	return 0
}

func runPeerServe(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("network peer serve", flag.ContinueOnError)
	set.SetOutput(stderr)
	configPath := set.String("config", "", "configuration YAML")
	listen := set.String("listen", ":8443", "listen address")
	tokenEnvironment := set.String("token-env", "", "peer token environment variable")
	if err := set.Parse(args); err != nil {
		return 2
	}
	if set.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "network peer serve accepts no positional arguments")
		return 2
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "peer server: %v\n", err)
		return 2
	}
	if *tokenEnvironment == "" {
		*tokenEnvironment = cfg.Network.Peer.TokenEnv
	}
	token, present := os.LookupEnv(*tokenEnvironment)
	if !present || token == "" {
		_, _ = fmt.Fprintf(stderr, "peer server: %s is not set\n", *tokenEnvironment)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	server := &peer.Server{Listen: *listen, Token: token}
	if err := server.Serve(ctx, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "peer server: %v\n", err)
		return 1
	}
	if ctx.Err() != nil {
		return 130
	}
	return 0
}

func JSONPlan(plan model.Plan) ([]byte, error) { return json.MarshalIndent(plan, "", "  ") }
