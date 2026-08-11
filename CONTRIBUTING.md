# Contributing

## Workflow

1. Start from the latest `develop` branch.
2. Use `feature/<LINEAR_ISSUE_ID>-<name>` for normal work.
3. Include the full Linear issue ID in commits and pull request titles.
4. Open the pull request against `develop` and wait for all required checks.
5. Use squash merge only.

Promotion pull requests move `develop` to `staging`, then `staging` to `main`.

## Verification

```sh
go test ./...
go vet ./...
go build ./cmd/lihacloud-bench
```

Generated GitHub Actions workflows must use the `.yaml` extension.
