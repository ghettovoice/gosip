# Completion Checks

- Follow `.agents/rules/proj_guide.md` and authoritative `Taskfile.yml` / `.golangci.yml`.
- Apply configured formatting with `golangci-lint fmt` to the intended scope; review the diff for unrelated changes.
- Run behavior-specific regression tests, then appropriate race/vet/coverage checks. Full repository test command: `go test -race -vet=all -timeout=1m -covermode=atomic -coverprofile=cover.out ./...` (or fresh `task --force test`). Narrow Task invocations and timeout overrides are described in `mem:suggested_commands`.
- After changing Go source/tests, lint config, or documentation code examples, run `golangci-lint run ./...` (or fresh `task --force lint`). Require successful exit and zero issues; re-run after fixes.
- Vulnerability gate: `govulncheck ./...` or fresh `task --force vuln`. `task check` sequences all three standard checks, but respect Task freshness and do not mistake an up-to-date skip for a fresh pass.
- Documentation-only edits: verify formatting, references and example consistency; do not claim tests ran when they did not.
- Preserve unrelated user changes and review the final diff. Report checks actually executed and any blocked/skipped checks.
- Memory-only onboarding: verify all required memories exist and memory references are valid; no Go behavior has changed. Use `serena memories check` for the reference audit.
