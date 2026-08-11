# Benchmark Contract

This document defines the public v1 command, workload, configuration, result,
and safety behavior. The Go package layout is internal and is not a public SDK.

## Commands

```text
lihacloud-bench init
lihacloud-bench doctor
lihacloud-bench plan
lihacloud-bench run
lihacloud-bench cpu
lihacloud-bench memory
lihacloud-bench disk
lihacloud-bench object s3
lihacloud-bench postgres pgbench
lihacloud-bench clickhouse clickbench
lihacloud-bench clickhouse clickcannon
lihacloud-bench network peer serve
lihacloud-bench network peer run
lihacloud-bench network iperf
lihacloud-bench network internet
lihacloud-bench cleanup --run-id <uuid>
lihacloud-bench cache status
lihacloud-bench cache prune
```

`run` selects the always-available CPU, memory, and disk categories plus every
external category enabled in configuration. `--only` restricts that set.
Category commands use the same planner, confirmation, result, and cleanup
pipeline as `run`.

Common flags are `--config`, `--profile standard|quick`, `--only`, `--output`,
`--yes`, and `--keep-resources`. `plan` and `doctor` are read-only. Mutating or
load-generating commands first print expected resource names, transfer sizes,
request counts, durations, external data sharing, and resolved engine versions.
An interactive terminal asks for confirmation; non-interactive execution
requires `--yes`.

## Configuration

The configuration schema is [config-v1.schema.json](../schemas/config-v1.schema.json).
Precedence is command flag, environment variable, YAML, then built-in default.
The YAML version must be `1`.

Credentials and credential-bearing URLs are never accepted in YAML. PostgreSQL
uses `LIHACLOUD_BENCH_POSTGRES_URL`, ClickHouse uses
`LIHACLOUD_BENCH_CLICKHOUSE_URL`, S3 uses the AWS SDK credential chain, and the
peer service uses `LIHACLOUD_BENCH_PEER_TOKEN`. Diagnostic and result values are
redacted before formatting or serialization.

## Profiles

### Standard

| Category | Workload |
| --- | --- |
| CPU | SHA-256 over deterministic 4 MiB blocks, one and all logical workers, 30 seconds and three trials each |
| Memory | Sequential read, write, and copy over min(25% available memory, 1 GiB), one and all workers, three trials |
| Disk | fio sequential 1 MiB read/write and random 4 KiB read/write, 60 seconds each; otherwise labeled buffered fallback |
| S3 | 1 GiB upload/download three times plus 10,000 4 KiB object operations at concurrency 1 and 32 |
| pgbench | Scale 100, 30-second warm-up and 120-second measured TPC-B-like, select-only, and simple-update at clients 1, vCPU, and min(2*vCPU, 64) |
| ClickBench | Pinned 100M-row dataset, load and data size, all 43 queries three times |
| ClickCannon | `otel_demo` traces, 15-minute generation/insertion and 15-minute query simulation with 10 users |
| Peer | TLS RTT and 30-second upload/download with 1 and 8 streams |
| iperf | TCP forward/reverse with 1 and 8 streams; configured UDP rate also records jitter and loss |
| Internet | One consent-gated M-Lab NDT7 download/upload run |

### Quick

Quick is a smoke profile and every result carries `comparable=false`: CPU,
memory, disk, and peer phases run for 5-10 seconds; S3 uses one 64 MiB transfer
and 100 small objects; pgbench uses scale 10 and 30-second runs at clients 1 and
vCPU; ClickBench loads the first partition and runs the first five queries once;
ClickCannon uses 60-second insert and query phases with two users.

## Result

Every attempted run writes an atomic JSON document conforming to
[result-v1.schema.json](../schemas/result-v1.schema.json). It includes schema,
tool, run and sanitized host metadata; the redacted plan; normalized benchmark
entries; engine and workload versions; parameters; metrics with units and
direction; warnings and stable error codes; resource ownership and cleanup
evidence. Raw tool output may be saved as a separate redacted artifact and is
referenced by path and checksum.

No hostname, IP address, credential, or composite score is collected by
default. A user-provided `host_label` is the only host identity field.

## Status and exit codes

- `success`: the selected benchmark completed.
- `skipped`: it was not configured or did not apply to the platform.
- `blocked`: it was configured but a required engine, consent, permission, or
  endpoint was unavailable.
- `failed`: execution started and the workload failed.

Exit 0 means every selected benchmark succeeded. Exit 1 means at least one
selected benchmark was blocked, failed, or left cleanup incomplete. Exit 2 is
reserved for usage, invalid configuration, and failures that prevent a plan.
Exit 130 indicates interruption after partial-result persistence and cleanup.

## Resource ownership and cleanup

Generated names are `lihacloud-bench-<run-id>` for S3 buckets and
`lihacloud_bench_<run-id-without-hyphens>` for databases. Every remote resource
contains a marker with schema version, run ID, creation time, and tool version.
The local run manifest is stored below the OS user cache directory.

Automatic cleanup requires all of the following: the resource name matches the
generated prefix and run ID, the local manifest records the resource, and the
remote marker matches the manifest. `cleanup` accepts only a run ID, never an
arbitrary remote name. Success, failure, and interruption attempt cleanup;
`--keep-resources` is the only retention override. Dataset/helper cache removal
is intentionally separate under `cache prune`.
