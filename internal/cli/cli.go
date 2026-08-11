package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/LIHA-Research/lihacloud-bench/internal/buildinfo"
)

const usage = `lihacloud-bench measures compute, storage, database, and network performance.

Usage:
  lihacloud-bench [command]

Commands will be added during the v0.1.0 implementation.

Flags:
  -h, --help      Show help
  -v, --version   Show version
`

// Run executes the command line and returns a process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		_, _ = io.WriteString(stdout, usage)
		return 0
	}

	if args[0] == "-v" || args[0] == "--version" || args[0] == "version" {
		_, _ = fmt.Fprintf(stdout, "lihacloud-bench %s (%s, %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return 0
	}

	_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n%s", strings.Join(args, " "), usage)
	return 2
}
