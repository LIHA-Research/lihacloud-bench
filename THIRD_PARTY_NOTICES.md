# Third-Party Notices

The core `lihacloud-bench` source is licensed under MIT. The following
components and workloads retain their own licenses.

## Distributed helper

- ClickCannon v0.4.0, Copyright ClickHouse contributors, Apache License 2.0.
  Source: https://github.com/ClickHouse/ClickCannon/tree/v0.4.0. Release binaries
  carry a minimal portability patch kept under `tools/clickcannon/patches`.

## Benchmark workload

- ClickBench at commit `e2fe0d3f4803cbf0a47068a9c7530b059a32d195`,
  Copyright ClickHouse contributors, Creative Commons
  Attribution-NonCommercial-ShareAlike 4.0 International. Source and license:
  https://github.com/ClickHouse/ClickBench. The embedded SQL query file is
  separately covered by that license; the benchmark dataset is downloaded from
  ClickHouse's published dataset service and is not redistributed here.

## Go dependencies

- AWS SDK for Go v2, Apache License 2.0.
- ClickHouse Go client and ch-go, Apache License 2.0.
- Other Go module versions are recorded exactly in `go.mod` and `go.sum`; their
  copyright and license notices remain in their respective source modules.
