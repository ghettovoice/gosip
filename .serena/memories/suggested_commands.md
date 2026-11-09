# Commands

Run from the repository root; `Taskfile.yml` is authoritative.

- `task test`: race detection, full vet, atomic coverage in `cover.out`, default per-package timeout `1m`. Direct equivalent: `go test -race -vet=all -timeout=1m -covermode=atomic -coverprofile=cover.out ./...`.
- `task lint` / `golangci-lint run ./...`: configured lint and formatter checks. Apply configured formatting using `golangci-lint fmt`, then re-run lint.
- `task vuln` / `govulncheck ./...`: vulnerability analysis. `task check` runs test, lint, then vuln.
- `PACKAGE` is relative to the module root, without a leading `./`; test/lint/vuln default to `...`. Example: `task test PACKAGE=sip TIMEOUT=2m COVERAGE_FILE=sip-cover.out -- -run TestTransaction`; `task lint PACKAGE=sip/transport`. Tool flags after `--` are forwarded through `CLI_ARGS`.
- Task test/lint/vuln use source-based up-to-date checks. For fresh evidence use `task --force test`, `task --force lint`, `task --force vuln`, or direct tool equivalents; skipped tasks are not fresh verification.
- `task cov` opens HTML coverage; `COVERAGE_FILE` defaults to `cover.out`. `task gen PACKAGE=...` runs `go generate ./...`.
- `task bench PACKAGE=sip COUNT=10` runs benchmarks with timestamped CPU/memory profiles and output; pass PACKAGE explicitly (the bench task has no PACKAGE default).
- Darwin-specific release implementation uses BSD `sed -i ''`. `task release VERSION=vX.Y.Z` edits the version, stages, commits and tags; it is not a read-only verification command.
- `task agent-links` restores links from `.devin/` to `.agents/` but removes non-symlink entries first; do not run casually.
- `serena memories check` checks memory references from the project root.
