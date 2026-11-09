# Toolchain

- Single Go module: `github.com/ghettovoice/gosip`; `go.mod` declares Go `1.27.0`. Use that declaration rather than assuming an older baseline.
- Go modules (`go.mod`, `go.sum`); Task v3 (`Taskfile.yml`) drives test/lint/vulnerability checks, benchmarks, coverage and generation.
- golangci-lint v2 configuration in `.golangci.yml`; configured formatters are `gci` and `gofumpt` with repository-specific options. `govulncheck` is a separate check.
- Core dependencies include `github.com/ghettovoice/abnf` (grammar), `codeberg.org/miekg/dns` (DNS), `github.com/qmuntal/stateless` (state machines), `github.com/jellydator/ttlcache/v3` (cache), and `braces.dev/errtrace` behind `internal/errors`. Consult `go.mod` for current dependency pins.
- Testing includes `github.com/google/go-cmp` and `go.uber.org/goleak`; logging integrates standard `log/slog`.
- `.github/workflows/test.yml` has separate lint, vulnerability and race/coverage test jobs; CI selects stable Go and latest golangci-lint rather than fixed tool versions.
- OpenSpec uses the `spec-driven` schema in `openspec/config.yaml`; change artifacts are under `openspec/changes/`.
