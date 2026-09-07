# Contributing

Thanks for taking an interest. This is a small, dependency-free Go project;
getting set up should take under a minute.

## Prerequisites

- Go 1.25 or newer
- `git`
- `kubectl` — only needed to exercise `cmd/health-watcher` against a real
  cluster. The test suite does not require it.

No Gemini API key is needed to build, test, or run the pipeline. The AI
layers are optional and degrade to deterministic behaviour without one.

## Build and test

All Go work happens inside the `gitops-ai-platform/` directory — that is
where `go.mod` lives.

```bash
cd gitops-ai-platform

make build     # go build ./...
make test      # go test -race ./...
make cover     # test with a coverage profile + summary
make lint      # gofmt check + go vet
make check     # everything CI runs
```

If you'd rather not use `make`, every target is a plain `go` command; read
the `Makefile`, it's short.

## Conventions

**Formatting.** `gofmt` is enforced in CI. Run `make fmt` before pushing.

**No third-party dependencies.** This module has an empty `require` block and
we would like to keep it that way. It's a deliberate constraint: it makes the
CLIs trivially vendorable into a distroless image and keeps the supply-chain
surface of a tool that writes to your deploy repo as small as possible. If you
genuinely need a dependency, open an issue first and make the case — the bar
is "the stdlib version would be materially wrong", not "this would be more
convenient".

**Tests.** New behaviour needs a test. Anything that would otherwise need a
live cluster or a live Gemini endpoint should be factored so the pure logic is
testable without one — `parsePodListJSON` and the `httptest`-backed Gemini
tests are the pattern to follow.

**Commits.** Conventional Commits (`feat:`, `fix:`, `docs:`, `chore:`,
`refactor:`, `test:`, `ci:`), with an optional scope matching the package —
e.g. `fix(gitopswriter): ...`. Write the body for someone reading `git log` in
a year: say what was wrong and why the fix is shaped the way it is, not just
what changed.

**Pull requests.** One logical change per PR. CI must be green: gofmt, vet,
build, race-enabled tests, `govulncheck`, and CodeQL.

## Project layout

```
gitops-ai-platform/
  cmd/          one thin main() per CLI — flag parsing and output only
  pkg/          all the actual logic, each package independently testable
  deploy/       Dockerfile for the CLIs + K8s manifests for health-watcher
  examples/     a sample app, including the workflow an onboarded repo runs
  docs/         architecture and deployment guides
```

Keep `cmd/` thin. If you find yourself writing logic in a `main()`, it belongs
in a package under `pkg/` where it can be tested.

## Reporting bugs

Open an issue with what you ran, what you expected, and what happened. For
anything security-sensitive, see [SECURITY.md](SECURITY.md) instead — please
don't open a public issue for it.
