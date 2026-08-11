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

## Build

The repository pins Go 1.26.5. A local Go installation is optional when Docker
is available:

```sh
docker run --rm -v "$PWD:/src" -w /src golang:1.26.5-bookworm go test ./...
docker run --rm -v "$PWD:/src" -w /src golang:1.26.5-bookworm \
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
