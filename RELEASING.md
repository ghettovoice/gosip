# Releasing a new version

## Pre-release checklist

Before releasing a new version, ensure all checks pass:

```bash
# Run all tests with race detection and vet checks
go test -race -vet=all ./...

# Run linter
golangci-lint run ./...

# Check for known vulnerabilities
govulncheck ./...
```

## Manual release

1. Update `VERSION` in `gosip.go`.
2. Commit, push, and release:

```bash
git commit -am "Release vM.m.p"
git tag vM.m.p
git push --follow-tags
```

## Automated release

```bash
make release VERSION=vM.m.p
```
