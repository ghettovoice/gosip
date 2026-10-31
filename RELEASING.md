# Releasing a new version

## Pre-release checklist

Before releasing a new version, ensure all checks pass:

```bash
# Run all tests with race detection and vet checks
task test

# Run linter
task lint

# Check for known vulnerabilities
task vuln

# All checks
task check
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
task release VERSION=vM.m.p
```
