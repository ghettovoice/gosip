# Releasing a new version

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
