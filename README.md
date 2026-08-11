# lihacloud-bench

Cross-platform benchmarks for compute, memory, storage, databases, and networks.

The project is under active development. The first release targets Linux, macOS,
and Windows on amd64 and arm64.

## Install

After the first tagged release:

```sh
go install github.com/LIHA-Research/lihacloud-bench/cmd/lihacloud-bench@latest
```

Prebuilt archives and checksums are published on the GitHub Releases page.

## Quick start

Create a secret-free configuration, inspect the resolved tools and workload,
then run the smoke profile:

```sh
lihacloud-bench init
lihacloud-bench doctor
lihacloud-bench plan --profile quick
lihacloud-bench run --profile quick --yes
```

Every run prints a terminal summary and atomically writes a versioned JSON
result. Non-interactive runs require `--yes`. Standard runs are intentionally
long and resource intensive; `plan` reports their estimated duration, transfer,
requests, and generated resources before execution.

Individual host and storage suites use the same planning and cleanup pipeline:

```sh
lihacloud-bench cpu --profile quick --yes
lihacloud-bench memory --profile quick --yes
lihacloud-bench disk --profile quick --yes
lihacloud-bench object s3 --profile quick --yes
lihacloud-bench postgres pgbench --profile quick --yes
lihacloud-bench clickhouse clickbench --profile quick --yes
lihacloud-bench clickhouse clickcannon --profile quick --yes
lihacloud-bench network iperf --profile quick --yes
lihacloud-bench network internet --profile quick --yes
```

S3-compatible storage uses the AWS SDK credential chain. Database URLs and the
peer token are read only from `LIHACLOUD_BENCH_POSTGRES_URL`,
`LIHACLOUD_BENCH_CLICKHOUSE_URL`, and `LIHACLOUD_BENCH_PEER_TOKEN`; credentials
are never stored in YAML or result JSON.

Disk tests prefer an installed `fio`. When it is unavailable, the CLI uses a
buffered built-in workload and marks the result `comparable=false`.

PostgreSQL requires `pgbench` and `psql` from the same client installation. The
ClickBench adapter pins its SQL workload to commit
`e2fe0d3f4803cbf0a47068a9c7530b059a32d195`, verifies cached dataset bytes, and
labels managed-service cache semantics explicitly. ClickCannon v0.4.0 is
released as a checksum-verified helper for every supported target; its native
connection requires a `clickhouse://`, `clickhouses://`, or `tcp://` URL.

For an authenticated peer test, set the same one-time
`LIHACLOUD_BENCH_PEER_TOKEN` on both hosts, start `network peer serve` on one
host, and copy its printed address and SHA-256 certificate fingerprint into the
client configuration (or `LIHACLOUD_BENCH_PEER_TARGET` and
`LIHACLOUD_BENCH_PEER_FINGERPRINT`). The client measures TLS RTT and 1/8-stream
upload and download throughput. Set `LIHACLOUD_BENCH_IPERF_TARGET` for iperf;
Unix uses iperf3 and Windows uses the officially supported iperf2 engine.
The engine distinction follows the
[iperf project Windows guidance](https://software.es.net/iperf/faq.html#windows).

The public internet test sends measurement data to M-Lab, which collects and
publishes the test IP address and measurement results under its
[privacy policy](https://www.measurementlab.net/privacy-v3/). It runs only when
`accept_mlab_data_policy: true` or
`LIHACLOUD_BENCH_ACCEPT_MLAB_DATA_POLICY=true` is set explicitly.

## Build

The repository pins Go 1.26.5. A local Go installation is optional when Docker
is available:

```sh
docker run --rm -e PATH=/usr/local/go/bin:$PATH -v "$PWD:/src" -w /src \
  golang:1.26.5-bookworm go test ./...
docker run --rm -e PATH=/usr/local/go/bin:$PATH -v "$PWD:/src" -w /src \
  golang:1.26.5-bookworm \
  go build ./cmd/lihacloud-bench
```

## Contracts

The public CLI, workload, configuration, result, and resource-safety contracts
are documented in [docs/benchmark-contract.md](docs/benchmark-contract.md).
Versioned JSON Schemas are available under [schemas](schemas/), and
[config.example.yaml](config.example.yaml) contains a secret-free starting
configuration.

## Development workflow

Changes are reviewed through `develop`, promoted to `staging`, and then released
from `main`. See [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## License

The project is licensed under the [MIT License](LICENSE). Third-party components
and workloads retain their own licenses; see
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
