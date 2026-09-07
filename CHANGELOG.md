# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For Helmsman, the public API that versioning applies to is: the CLI flags of
the seven commands, the `platform.yaml` schema, the shape of the generated
Kubernetes manifests, and the layout of what is written into the GitOps repo
(`apps/<app>/<env>/…`). Exported Go identifiers are versioned too, but the
CLIs are what most people depend on.

## [Unreleased]

### Changed

- **Minimum Go is now 1.25.** The first CI run on the merged branches failed
  `govulncheck` with 25 reachable standard-library advisories — quadratic
  `net/url` path resolution, unbounded post-handshake messages and an ECH
  privacy leak in `crypto/tls`, unbounded recursion in `encoding/asn1`,
  quadratic name-constraint checking in `crypto/x509` — all of them present in
  the Go 1.23 standard library and fixed in 1.24.8 through 1.25.13. Nothing in
  this repository was at fault; the toolchain floor was.

  Raising it was the honest fix rather than an exclusion list. This project
  ships a blocking vulnerability gate as a feature, so declaring a minimum
  below the line where those advisories are fixed would mean `make vuln` fails
  for anyone building on the version we advertise.

- **`govulncheck` is pinned to v1.7.0 and no longer installed with `@latest`.**
  `@latest` resolved to a release requiring a newer Go than the module
  declared, so the toolchain silently switched mid-job — meaning the scan
  examined a different standard library than the one CI actually builds and
  ships with. It now runs via `go run …@v1.7.0`, with the version set once in
  the Makefile.

### Fixed

- **The Pages deploy could never run on a fresh repository.**
  `actions/configure-pages` failed with `Get Pages site failed … Not Found`
  because Pages had not been switched on by hand in Settings. It now passes
  `enablement: true`, so the workflow turns Pages on itself.

## [1.0.0] - 2026-09-07

First tagged release. The pipeline was feature-complete before this; what
1.0.0 adds is the evidence that it works and the packaging to install it.

### Added

- **Helmsman branding** — the project has a name, a mark, a wordmark and a
  colour system: black, white and the greys between them, and no hue at all.
  Emphasis comes from brightness and weight, which is also how the site
  distinguishes the two AI-assisted pipeline stages from the four
  deterministic ones.
- **Landing page** at
  <https://zishaan1911.github.io/Helmsman/> —
  a single self-contained page, deployed by a workflow that verifies every
  local asset reference resolves before publishing.
- **`-version` on every CLI**, with the release tag, commit SHA, build date,
  Go version and platform. Values are injected at link time by GoReleaser and
  by `deploy/Dockerfile`; an unstamped `go build` honestly reports `dev`, and
  `go install …@v1.0.0` falls back to the VCS stamps the Go toolchain embeds.
- **Signed, reproducible release artifacts** — GoReleaser builds all seven
  CLIs for linux/darwin/windows on amd64 and arm64, with `-trimpath` and
  pinned `mod_timestamp` so the same commit produces the same bytes. Each
  archive carries all seven binaries plus the docs, the `health-watcher`
  manifests and an example `platform.yaml`. SHA-256 checksums are published
  alongside.
- **Multi-arch container image** at `ghcr.io/zishaan1911/helmsman`, built for
  linux/amd64 and linux/arm64, smoke-tested in CI before publication: every
  binary reports its version, `git` and `kubectl` are present, and the image
  is verified to run as UID 10001 rather than root.
- **End-to-end tests** (`internal/e2e`) that wire the real packages together
  in `cmd/pipeline`'s order and assert on what lands in a throwaway GitOps
  repo — the happy path, idempotency, environment isolation, the crash-loop
  rollback path, and a risky manifest being blocked before commit. They run
  against `examples/sample-app`, so the bundled example is now verified on
  every CI run.
- **Tests for `pkg/containerizer`, `pkg/gitopswriter` and
  `pkg/platformconfig`**, which had none. Coverage over `./pkg/...` is 85.7%,
  up from 27.5% across the module, and CI fails below 80%.
- **A `Makefile`** — `build`, `binaries`, `test`, `cover`, `cover-check`,
  `fmt`, `lint`, `vuln`, `tidy`, `check`, and `demo`, which runs the whole
  pipeline against the bundled example in a scratch GitOps repo.
- **Root `README`, `CONTRIBUTING`, `SECURITY`, `CODE_OF_CONDUCT`**, GitHub
  issue forms and a pull request template. `SECURITY.md` states the threat
  model plainly: the CLIs never mutate a cluster, so the real blast radius is
  write access to a GitOps repository.

### Fixed

- **Rollback with a custom message never worked.** `RevertLastCommit` passed
  `CommitMessage` to `git revert -m`, where `-m` selects the mainline parent
  of a merge. git rejects a non-numeric argument outright, so every rollback
  that set a message failed — and only ever in the situation the platform
  exists to handle. The revert is now staged with `--no-commit` and committed
  separately, with `git revert --quit` clearing sequencer state if that fails,
  so a half-applied revert can't block the next one.
- **Spring Boot went undetected in Gradle projects.** `detectJava` matched
  only the literal `spring-boot`, which appears in Maven POMs but not in
  Gradle builds, where the plugin is applied as `org.springframework.boot`.
  Separately, `build.gradle.kts` was recognised as a Java marker but never
  actually read. Both spellings and both DSL filenames are now handled.
- **`gitopswriter` printed raw git output to stdout**, interleaving it with
  the structured summary `cmd/pipeline` writes — while the returned error
  carried only `exit status 1`. Output is now captured and folded into the
  error, where it is useful.
- **Dependabot never checked this module's dependencies.** Its `gomod`
  ecosystem pointed at `/`, where there is no `go.mod`. Now pointed at
  `/gitops-ai-platform`, with a `docker` ecosystem added for `deploy/`.
- **CodeQL would have failed on the next push**, still pinning Go 1.22 after
  `go.mod` moved to 1.23.

### Changed

- **`govulncheck` blocks again.** It was marked `continue-on-error` as a
  "TEMPORARY" measure while Go 1.22 standard-library findings were triaged.
  The toolchain has since moved past them, and a vulnerability gate that
  cannot fail is not a gate.
- **The module path is real.** It was `github.com/example/gitops-ai-platform`,
  a scaffold placeholder, so `go get` and `go install` could not resolve it.
- **CI runs a matrix** — Go 1.23 and 1.24 on Linux and macOS, with `-race`,
  a coverage gate, and a smoke job that runs the pipeline end to end and
  asserts the checkout was left unmodified.
- **`deploy/Dockerfile` is reproducible and multi-arch.** It pinned Go 1.22
  and resolved `https://dl.k8s.io/release/stable.txt` at build time, so the
  same Dockerfile produced a different image every week. `kubectl` is now
  pinned and checksum-verified, the build cross-compiles from the native
  toolchain, and the version is stamped into the binaries.

### Removed

- `PROJECT_COMPLETE.md`, which declared the repository closed and suggested
  archiving it.
- `gitops-ai-platform/github/`, a copy of `.github/` committed without the
  leading dot. GitHub never read it, so it drifted — still pinning Go 1.22
  and missing every `working-directory` fix the live workflows had received.

[Unreleased]: https://github.com/zishaan1911/Helmsman/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/zishaan1911/Helmsman/releases/tag/v1.0.0
