# Helmsman — `gitops-ai-platform`

The Go module behind [Helmsman](https://zishaan1911.github.io/Helmsman/).
Push code → get a running app, deployed via GitOps, watched and self-healed by AI.

```
detect → containerize → generate manifests → AI risk review → commit to GitOps repo
   → (ArgoCD/Flux syncs) → health-watcher monitors → unhealthy? AI root cause + auto-rollback
```

Full roadmap (see `docs/ARCHITECTURE.md`) is implemented, weeks 1–10:

- **Weeks 1–6**: detection, containerization, manifest generation, GitOps commit — all deterministic, template-based. See prior sections below.
- **Weeks 7–8**: `pkg/riskreview` — static policy checks (resource limits, zero replicas, privileged containers, replica-jump blast radius) plus a Gemini-backed layer for contextual judgment calls (suspicious env vars, unusual diffs). AI findings are strictly additive to static findings and never silence them; if Gemini is unreachable, review fails closed to static-only rather than skipping review.
- **Weeks 9–10**: `pkg/healthwatcher` — watches rollout health via `kubectl`, and on failure gathers pod/event evidence, asks Gemini for a plain-English root cause, and can auto-rollback by `git revert`-ing the last GitOps commit (never a direct cluster mutation).

**Deploying this for real:** see `DEPLOYMENT.md`.

## Components

| Path | What it does | AI involved? |
|---|---|---|
| `pkg/detector` | Inspects a repo, infers language/framework/entrypoint/port | No |
| `pkg/containerizer` | Generates a Dockerfile from the detected service | No |
| `pkg/platformconfig` | Reads optional `platform.yaml` overrides from the app repo | No |
| `pkg/manifest` | Generates Deployment/Service/Ingress/Kustomization YAML | No |
| `pkg/gitopswriter` | Commits manifests to a GitOps repo; renders the ArgoCD `Application`; reverts commits for rollback | No |
| `pkg/gemini` | Minimal stdlib-only client for Gemini's structured-JSON output | — |
| `pkg/riskreview` | Scores a manifest diff for risk before it's allowed to merge | Yes |
| `pkg/healthwatcher` | Watches rollout health, root-causes failures, triggers rollback | Yes |
| `cmd/pipeline` | Runs detect → containerize → manifest → commit end-to-end | |
| `cmd/risk-reviewer` | Standalone CLI for the risk gate — meant to run as a CI/PR check | |
| `cmd/health-watcher` | Standalone CLI — meant to run as a cluster CronJob/ArgoCD PostSync hook | |
| `cmd/detector`, `cmd/containerizer`, `cmd/manifest-generator`, `cmd/gitops-writer` | Same stages as standalone CLIs, for scripting/debugging one stage at a time | |
| `deploy/` | Dockerfile for the platform's own CLIs, plus K8s manifests (RBAC, CronJob) for `health-watcher` | |
| `examples/sample-app` | A minimal Express app, including the GitHub Actions workflow an onboarded app repo actually runs | |

## Quickstart (local, no cluster required)

```bash
go build -o bin/pipeline ./cmd/pipeline

# Set up a local GitOps repo (in real usage this is your existing ArgoCD/Flux-watched repo)
git init /tmp/gitops-repo && cd /tmp/gitops-repo && git commit --allow-empty -m init

./bin/pipeline \
  -app-repo ./examples/sample-app \
  -gitops-repo /tmp/gitops-repo \
  -app sample-app \
  -image registry.example.com/sample-app:sha-abc123 \
  -env staging \
  -gitops-repo-url https://github.com/you/gitops-repo.git
```

This will:
1. Detect the app is Node/Express, listening on the port from `platform.yaml` (or a framework default)
2. Write a multi-stage, non-root `Dockerfile` into `examples/sample-app/`
3. Generate `deployment.yaml`, `service.yaml`, `ingress.yaml` (if `public: true`), `kustomization.yaml`
4. Commit them to `/tmp/gitops-repo/apps/sample-app/staging/`
5. Print the one-time `ArgoCD Application` resource to onboard the app

From there, ArgoCD/Flux — already watching that GitOps repo, unmodified —
picks up the commit and reconciles the cluster. This platform never talks
to the Kubernetes API directly; it only writes and commits files.

## Risk review (Week 7-8)

```bash
export GEMINI_API_KEY=...  # optional; static checks run without it
go build -o bin/risk-reviewer ./cmd/risk-reviewer
./bin/risk-reviewer -app sample-app -new /tmp/gitops-repo/apps/sample-app/staging/deployment.yaml
echo $?   # non-zero if risk score >= riskreview.ApprovalThreshold (60 by default)
```

## Health watching + rollback (Week 9-10)

Requires a real cluster with `kubectl` configured (not exercisable in a sandbox without one):

```bash
go build -o bin/health-watcher ./cmd/health-watcher
./bin/health-watcher -namespace sample-app -app sample-app \
  -gitops-repo /tmp/gitops-repo -auto-rollback -push
```

## `platform.yaml` (optional, lives in the app repo)

```yaml
port: 4000
replicas: 3
public: true
resources:
  cpuRequest: "150m"
  memoryRequest: "192Mi"
  cpuLimit: "750m"
  memoryLimit: "768Mi"
env:
  - name: LOG_LEVEL, value: info
  - name: DATABASE_URL, secretRef: sample-app-secrets/database-url
```

Every field is optional — omitted fields fall back to platform defaults
(2 replicas, conservative resource requests/limits, `public: false`).

## What's deliberately out of scope here

- **Actual `docker build`/registry push** — wrap Kaniko or BuildKit in CI (see the example workflow); not reimplemented here.
- **Actual ArgoCD/Flux install or sync** — this repo produces the `Application` resource and the GitOps commits; reconciliation is ArgoCD/Flux's job, untouched. Install steps are in `DEPLOYMENT.md`.
- **A general-purpose K8s API client** — `health-watcher` shells out to `kubectl` rather than depending on `client-go`, to keep the module dependency-free.

## Testing

```bash
make check    # gofmt + vet + build + race-enabled tests
make cover    # the same, with a coverage profile and summary
make demo     # run the whole pipeline against examples/sample-app
```

`make help` lists every target. All of them are plain `go` commands; the
Makefile just stops them from being retyped differently in four places.

The whole suite runs with no cluster and no API key. `pkg/gemini`,
`pkg/riskreview` and `pkg/healthwatcher` mock the Gemini API with `httptest` or
feed synthetic `kubectl`-shaped JSON; `pkg/gitopswriter` and `internal/e2e`
drive a real throwaway Git repo in a temp directory.

`internal/e2e` wires the packages together in `cmd/pipeline`'s order and runs
them against `examples/sample-app`, so the example is verified on every run
rather than only when someone remembers to try it by hand.
