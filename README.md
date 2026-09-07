# AI-Powered GitOps Deployment Platform

Push code → get a running app, deployed via GitOps, watched and self-healed by AI.

```
detect → containerize → generate manifests → AI risk review → commit to GitOps repo
   → (ArgoCD/Flux syncs) → health-watcher monitors → unhealthy? AI root cause + auto-rollback
```

The platform is a set of small Go CLIs, not a server. They run in your app
repos' CI and as a CronJob in your cluster. **Nothing here talks to the
Kubernetes API to make changes** — every change is a commit to a GitOps repo
that ArgoCD or Flux reconciles, so every deploy is auditable, revertible, and
already covered by whatever branch protection your org runs.

## Where things live

The Go module lives in [`gitops-ai-platform/`](gitops-ai-platform/). Start
there:

| Document | What it covers |
|---|---|
| [`gitops-ai-platform/README.md`](gitops-ai-platform/README.md) | Package-by-package tour, quickstart, `platform.yaml` reference |
| [`gitops-ai-platform/docs/ARCHITECTURE.md`](gitops-ai-platform/docs/ARCHITECTURE.md) | Why the pipeline is shaped this way, and where AI is and isn't used |
| [`gitops-ai-platform/docs/DEPLOYMENT.md`](gitops-ai-platform/docs/DEPLOYMENT.md) | Taking it from this repo to a real cluster |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Build, test, and PR conventions |
| [`SECURITY.md`](SECURITY.md) | Reporting a vulnerability |

## Quickstart

```bash
git clone https://github.com/zishaan1911/Helmsman.git
cd Helmsman/gitops-ai-platform
go build ./...
go test ./...
```

Run the full pipeline against the bundled example app, with a throwaway
GitOps repo — no cluster and no API key required:

```bash
git init /tmp/gitops-repo && git -C /tmp/gitops-repo commit --allow-empty -m init

go run ./cmd/pipeline \
  -app-repo ./examples/sample-app \
  -gitops-repo /tmp/gitops-repo \
  -app sample-app \
  -image registry.example.com/sample-app:sha-abc123 \
  -env staging \
  -gitops-repo-url https://github.com/you/gitops-repo.git

git -C /tmp/gitops-repo show --stat
```

## Where AI is used — and where it deliberately isn't

The interesting design decision in this project is how little of it is AI.
Language detection, Dockerfile generation, and manifest rendering are all
deterministic and template-driven, because they are solved problems and a
probabilistic answer there is strictly worse than a correct one.

AI (Gemini) is used in exactly two places, both of them judgment calls a
static rule cannot encode:

- **Risk review** (`pkg/riskreview`) — contextual findings on a manifest diff,
  layered *on top of* static policy checks. AI findings can only add to the
  static ones; they can never silence one, and if Gemini is unreachable the
  review falls back to static-only rather than passing unreviewed.
- **Root-cause analysis** (`pkg/healthwatcher`) — turning pod statuses and
  cluster events into a plain-English explanation for the developer who
  shipped the change. If the call fails, a templated fallback is emitted, so
  on-call never gets a blank issue body.

Rollback itself is not an AI decision: it is a `git revert` in the GitOps
repo, triggered by deterministic health rules.

## Status

All ten weeks of the original roadmap are implemented. See
[`CONTRIBUTING.md`](CONTRIBUTING.md) if you want to build on it.

## License

[MIT](LICENSE)
