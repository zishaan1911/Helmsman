# Deploying gitops-ai-platform

This covers taking the platform from "code in this repo" to "developers push, AI handles the rest, ArgoCD deploys it." It assumes you already have a Kubernetes cluster; if not, get one first (GKE/EKS/AKS, or `kind`/`k3d` for a test run).

## 0. What you're deploying, concretely

There is no single "platform server" to run. The platform is three things:

1. **CLI binaries** (`pipeline`, `risk-reviewer`, `health-watcher`, etc.) — run inside your **app repos' CI** (GitHub Actions) on every push/PR.
2. **A GitOps repo** — plain Git, watched by ArgoCD. The CLIs commit to it; they never touch the cluster.
3. **ArgoCD** (or Flux) — reconciles the GitOps repo into the cluster, and runs `health-watcher` as a CronJob/Job inside the cluster to watch rollouts.

Nothing here is a long-running service you expose publicly — it's CI jobs + a cluster-internal CronJob.

---

## 1. Install ArgoCD (skip if you already run it)

```bash
kubectl create namespace argocd
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml

# Get the initial admin password
kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath="{.data.password}" | base64 -d
```

Port-forward to check it's up:

```bash
kubectl -n argocd port-forward svc/argocd-server 8080:443
# https://localhost:8080, user: admin
```

## 2. Create the GitOps repo

A plain, empty Git repo — nothing platform-specific about it yet:

```bash
gh repo create your-org/gitops-repo --private --clone
cd gitops-repo && git commit --allow-empty -m "init" && git push
```

The platform will create `apps/<name>/<env>/{deployment,service,ingress,kustomization}.yaml` under this repo as apps onboard.

Generate a token ArgoCD and your CI can both use to read/write it (a GitHub fine-grained PAT scoped to just this repo, or a deploy key). Store it as `GITOPS_REPO_TOKEN` in each app repo's secrets (step 5) and give ArgoCD read access to it (step 3).

## 3. Get the platform image

Released images are published to GHCR for linux/amd64 and linux/arm64:

```bash
docker pull ghcr.io/zishaan1911/helmsman:v1.0.0
```

Pin the tag rather than tracking `latest`. This image writes to your deploy
repo; you want to know exactly which build did it, and `helmsman:v1.0.0`
answers that while `helmsman:latest` does not.

The image contains all seven CLIs at `/app/<name>` — `pipeline`,
`risk-reviewer`, `health-watcher`, `detector`, `containerizer`,
`manifest-generator`, `gitops-writer` — plus `git` and a pinned `kubectl`.
CI workflows and the in-cluster CronJob both pull this one image. Every
binary answers `-version` with its tag, commit and build date, which is the
first thing to check when a manifest looks wrong.

If you'd rather mirror it into your own registry — most organisations
should, so a cluster rollout doesn't depend on GHCR being reachable:

```bash
docker pull ghcr.io/zishaan1911/helmsman:v1.0.0
docker tag  ghcr.io/zishaan1911/helmsman:v1.0.0 registry.your-org.com/helmsman:v1.0.0
docker push registry.your-org.com/helmsman:v1.0.0
```

Or build it yourself from a checkout, stamping the version so the binaries
still identify themselves:

```bash
cd gitops-ai-platform
docker build -f deploy/Dockerfile \
  --build-arg VERSION="$(git describe --tags --always)" \
  --build-arg COMMIT="$(git rev-parse HEAD)" \
  --build-arg DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t registry.your-org.com/helmsman:local .
```

## 4. Deploy `health-watcher` into the cluster

```bash
kubectl apply -f deploy/manifests/health-watcher-rbac.yaml

# Fill in real secrets before applying:
kubectl create secret generic gitops-ai-secrets -n gitops-ai-platform \
  --from-literal=gemini-api-key="$GEMINI_API_KEY" \
  --from-literal=git-token="$GITOPS_REPO_TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl apply -f deploy/manifests/health-watcher-cronjob.yaml
```

The shipped CronJob watches one app (`sample-app`) every 5 minutes as a starting template — duplicate the `CronJob` block per app you onboard, or template it with Kustomize/Helm once you have more than a couple. **Better long-term option:** wire `health-watcher` as an [ArgoCD PostSync hook](https://argo-cd.readthedocs.io/en/stable/user-guide/resource_hooks/) on each `Application` instead of polling — it fires immediately after sync rather than up to 5 minutes later. The CronJob is the fastest path to something working today.

## 5. Onboard your first app

In the **app repo** (not this platform repo):

1. Copy `examples/sample-app/.github/workflows/deploy.yml` into `.github/workflows/`.
2. Update `APP_NAME`, `GITOPS_REPO`, and `IMAGE` at the top of the file.
3. Add repo secrets: `GITOPS_REPO_TOKEN`, `GEMINI_API_KEY`, `REGISTRY_USERNAME`, `REGISTRY_PASSWORD`.
4. (Optional) add a `platform.yaml` at the repo root — see the README for the schema. Skip it entirely and the platform uses safe defaults (2 replicas, conservative resource limits, not publicly exposed).
5. Push to `main`.

That push will: detect the language, generate a Dockerfile, build and push the image, generate K8s manifests, run the risk gate, and commit to the GitOps repo.

## 6. Point ArgoCD at the app

One-time per app — apply the `Application` the pipeline printed on that first run (or generate it yourself):

```bash
go run ./cmd/gitops-writer -h   # or use pkg/gitopswriter.ArgoApplication directly
```

Or just write it by hand from the template `pkg/gitopswriter.ArgoApplication` produces:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: sample-app-staging
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/your-org/gitops-repo.git
    targetRevision: main
    path: apps/sample-app/staging
  destination:
    server: https://kubernetes.default.svc
    namespace: sample-app
  syncPolicy:
    automated: { prune: true, selfHeal: true }
    syncOptions: [CreateNamespace=true]
```

```bash
kubectl apply -f sample-app-application.yaml -n argocd
```

ArgoCD will sync within seconds (or immediately if you trigger a manual sync in the UI/`argocd app sync sample-app-staging`).

## 7. Verify

```bash
argocd app get sample-app-staging          # sync status
kubectl get pods -n sample-app             # your app running
kubectl get events -n gitops-ai-platform   # health-watcher CronJob runs
```

Push a bad change (e.g. set `replicas: 0` in `platform.yaml`) and confirm the risk gate blocks it in CI before it ever reaches the GitOps repo.

---

## Rollback, manually

Rollback in this platform is always "revert the Git commit," never a direct cluster mutation:

```bash
cd gitops-repo
git revert --no-edit HEAD
git push
```

ArgoCD reconciles the revert automatically. `health-watcher -auto-rollback` does exactly this when it detects an unhealthy rollout.

## Local testing without a cluster

Every CLI works standalone against local paths — you don't need a cluster to iterate on detection/containerization/manifest logic:

```bash
go build -o bin/pipeline ./cmd/pipeline
git init /tmp/gitops-repo && (cd /tmp/gitops-repo && git commit --allow-empty -m init)
./bin/pipeline -app-repo ./examples/sample-app -gitops-repo /tmp/gitops-repo \
  -app sample-app -image registry.example.com/sample-app:dev -env staging
```

`health-watcher` and `risk-reviewer`'s AI layer need `GEMINI_API_KEY`; without it, `risk-reviewer` still runs (static checks only) and `health-watcher`'s root-cause step falls back to a templated message instead of an AI explanation — neither blocks on a missing key.
