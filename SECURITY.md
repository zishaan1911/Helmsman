# Security Policy

## Supported versions

| Version | Supported |
|---|---|
| 1.x | ✅ |
| < 1.0 | ❌ |

## Reporting a vulnerability

Please **do not open a public issue** for a security problem.

Report it privately through GitHub's
[private vulnerability reporting](https://github.com/zishaan1911/Helmsman/security/advisories/new)
on this repository, or by email to iamzishaan@gmail.com.

Include what you can: affected version or commit, reproduction steps, and
what an attacker gains. You should get an acknowledgement within 72 hours and
an assessment within 7 days. If a fix is warranted, it ships as a patch
release with an advisory crediting you unless you'd rather stay anonymous.

## What this project's threat model actually is

Worth being concrete, because "a deployment platform" sounds scarier than
what is running here:

- The CLIs **never authenticate to or mutate a Kubernetes cluster**. They
  write files and make Git commits. `health-watcher` is the only component
  that reads from a cluster, and it does so through whatever `kubectl`
  context it is handed — typically a ServiceAccount scoped to one namespace
  (see `deploy/manifests/health-watcher-rbac.yaml`).
- The blast radius of a compromise is therefore **write access to a GitOps
  repository**, gated by whatever branch protection and review rules that
  repo already enforces. Configure them; this platform is designed to be run
  behind them, not instead of them.
- `GEMINI_API_KEY` is read from the environment and sent only to
  `generativelanguage.googleapis.com`. Manifests and pod statuses are
  included in those prompts — **do not put literal secrets in manifests**.
  Use `secretRef` in `platform.yaml` so secret values stay in Kubernetes
  Secrets and never enter a prompt.
- The module has no third-party Go dependencies, so its supply-chain surface
  is the Go standard library plus the base images named in `deploy/`.

## Things that are known and intentional

These are not vulnerabilities; please don't report them as such.

- `pkg/platformconfig` parses a deliberately tiny subset of YAML by hand
  rather than depending on a YAML library. It is not a general YAML parser
  and makes no attempt to be one.
- `pkg/riskreview`'s static checks are string and regex matches against the
  exact manifest shape `pkg/manifest` produces. They are a policy gate for
  this platform's own output, **not** a general-purpose Kubernetes linter.
  For untrusted or hand-written manifests, run OPA/Gatekeeper or Kyverno as
  well — the package doc says so too.
- `health-watcher` shells out to `kubectl` instead of linking `client-go`.
