# Contributing to gox-packages

## Adding a new package

1. Create `libs/<name>/go.mod` with module path `github.com/thescaffold/gox-packages/libs/<name>`.
2. Add the module path to `go.work`.
3. Add a README at `libs/<name>/README.md`.
4. Add the package to the CI matrix in `.github/workflows/ci.yml`.

## Modifying an existing package

- Packages are imported by all 19 services in gox-apps — breaking changes require coordinating all consumers.
- Prefer adding new symbols to extending existing ones when backwards compatibility matters.
- Run `go build ./...` and `go test ./...` from the package directory before opening a PR.

## Publishing a release

Tag releases using the format `libs/<package>/v<semver>` — the nested-module
form Go requires for a module that isn't at the repo root. A bare
`<package>/v<semver>` tag addresses the repo-root module (which doesn't
exist here) instead, and produces a confusing "unknown revision"/module
lookup error unrelated to the actual tag:

```bash
git tag libs/core/v1.1.0
git push origin libs/core/v1.1.0
```

The publish workflow will build, test, and create a GitHub Release automatically.

## Code style

- Standard Go formatting (`gofmt`).
- No comments unless the WHY is non-obvious.
- Table-driven tests in `tests/` using `testing.T`.
- All exported symbols must be usable without reading source.
