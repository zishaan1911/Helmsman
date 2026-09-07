// Package e2e wires the real packages together in the same order
// cmd/pipeline does and asserts on what lands in the GitOps repo.
//
// Every other test in this module is a unit test with a synthetic fixture.
// Those cannot catch the failures that actually matter here: a stage whose
// output no longer satisfies the next stage's assumptions. The detector
// resolving a port the manifest never uses, the manifest drifting out of
// the shape riskreview greps for, the ArgoCD Application pointing at a
// directory the writer doesn't create — each of those passes every unit
// test and produces a broken deploy.
//
// It runs against examples/sample-app, so the example is exercised on every
// CI run rather than rotting quietly.
package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/containerizer"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/detector"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/gitopswriter"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/healthwatcher"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/manifest"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/platformconfig"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/riskreview"
)

// sampleAppCopy copies examples/sample-app somewhere writable. The
// containerizer writes a Dockerfile into the app repo it is given, and a
// test must not mutate the checkout it is running from.
func sampleAppCopy(t *testing.T) string {
	t.Helper()

	src := filepath.Join("..", "..", "examples", "sample-app")
	if _, err := os.Stat(filepath.Join(src, "package.json")); err != nil {
		t.Fatalf("examples/sample-app not found at %s: %v", src, err)
	}

	dst := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("reading %s: %v", src, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue // the example's nested .github/ isn't needed here
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			t.Fatalf("writing %s: %v", e.Name(), err)
		}
	}
	return dst
}

func newGitopsRepo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}

	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "platform@example.com")
	git(t, dir, "config", "user.name", "gitops-ai-platform")
	git(t, dir, "config", "commit.gpgsign", "false")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// runPipeline mirrors cmd/pipeline's stage order exactly.
func runPipeline(t *testing.T, appRepo, gitopsRepo, appName, env, image string) (detector.ServiceInfo, platformconfig.Config, manifest.Output, gitopswriter.WriteResult) {
	t.Helper()

	info, err := detector.Detect(appRepo)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}

	cfg, err := platformconfig.Load(appRepo, info.Port)
	if err != nil {
		t.Fatalf("platformconfig: %v", err)
	}

	if _, err := containerizer.Generate(info, appRepo); err != nil {
		t.Fatalf("containerize: %v", err)
	}

	manifests, err := manifest.Generate(manifest.Input{
		AppName:   appName,
		Namespace: appName,
		Image:     image,
		Env:       env,
		Service:   info,
		Config:    cfg,
	})
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}

	res, err := gitopswriter.Write(gitopswriter.WriteRequest{
		GitopsRepoPath: gitopsRepo,
		AppName:        appName,
		Env:            env,
		Manifests:      manifests,
	})
	if err != nil {
		t.Fatalf("gitops write: %v", err)
	}

	return info, cfg, manifests, res
}

func TestPipeline_SampleAppEndToEnd(t *testing.T) {
	appRepo := sampleAppCopy(t)
	gitopsRepo := newGitopsRepo(t)

	info, cfg, manifests, res := runPipeline(t, appRepo, gitopsRepo, "sample-app", "staging", "registry.example.com/sample-app:sha-abc123")

	t.Run("detection", func(t *testing.T) {
		if info.Language != "node" || info.Framework != "express" {
			t.Errorf("detected %s/%s, want node/express", info.Language, info.Framework)
		}
		if info.Confidence != "high" {
			t.Errorf("Confidence = %q, want high for the bundled example", info.Confidence)
		}
	})

	// The example ships a platform.yaml. Its values have to win over the
	// detector's guesses, or the override mechanism is decorative.
	t.Run("platform.yaml overrides the detector", func(t *testing.T) {
		if cfg.Port != 4000 {
			t.Errorf("Port = %d, want 4000 from platform.yaml", cfg.Port)
		}
		if cfg.Replicas != 3 {
			t.Errorf("Replicas = %d, want 3 from platform.yaml", cfg.Replicas)
		}
		if !cfg.Public {
			t.Error("the example declares public: true")
		}
	})

	t.Run("the resolved port reaches every stage", func(t *testing.T) {
		dockerfile, err := os.ReadFile(filepath.Join(appRepo, "Dockerfile"))
		if err != nil {
			t.Fatalf("reading generated Dockerfile: %v", err)
		}
		if !strings.Contains(string(dockerfile), "EXPOSE 4000") {
			t.Errorf("Dockerfile does not EXPOSE the configured port:\n%s", dockerfile)
		}
		if !strings.Contains(manifests.Deployment, "containerPort: 4000") {
			t.Error("Deployment does not use the configured port")
		}
		if !strings.Contains(manifests.Service, "targetPort: 4000") {
			t.Error("Service does not target the configured port")
		}
	})

	t.Run("manifests are committed", func(t *testing.T) {
		if !res.Committed || res.CommitSHA == "" {
			t.Fatalf("nothing was committed: %+v", res)
		}
		for _, name := range []string{"deployment.yaml", "service.yaml", "ingress.yaml", "kustomization.yaml"} {
			path := filepath.Join(gitopsRepo, "apps", "sample-app", "staging", name)
			if _, err := os.Stat(path); err != nil {
				t.Errorf("expected %s in the GitOps repo: %v", name, err)
			}
		}
		if status := git(t, gitopsRepo, "status", "--porcelain"); status != "" {
			t.Errorf("GitOps repo is dirty after the pipeline ran:\n%s", status)
		}
	})

	// The platform's own output must clear the platform's own gate. If it
	// doesn't, every deploy needs a human override and the gate is noise.
	t.Run("output passes the static risk gate", func(t *testing.T) {
		review := riskreview.Run(context.Background(), nil, "sample-app", manifests.Deployment, "")
		if !review.Approved {
			t.Errorf("our own generated manifest failed the risk gate: score=%d findings=%+v", review.RiskScore, review.Findings)
		}
		if review.AIReviewed {
			t.Error("AIReviewed = true with a nil client")
		}
	})

	t.Run("the ArgoCD Application points at the committed path", func(t *testing.T) {
		app := gitopswriter.ArgoApplication("sample-app", "staging", "https://github.com/you/gitops-repo.git", "main", "sample-app")

		rel, err := filepath.Rel(gitopsRepo, res.AppDir)
		if err != nil {
			t.Fatalf("computing relative path: %v", err)
		}
		if !strings.Contains(app, "path: "+filepath.ToSlash(rel)) {
			t.Errorf("Application path does not match the committed directory %q:\n%s", rel, app)
		}
	})
}

// Re-running the pipeline on an unchanged repo must be a no-op. The
// workflow in examples/sample-app runs on every push, so anything else
// fills the GitOps repo with empty commits and makes its history useless
// for working out which change broke a deploy.
func TestPipeline_IsIdempotent(t *testing.T) {
	appRepo := sampleAppCopy(t)
	gitopsRepo := newGitopsRepo(t)
	const image = "registry.example.com/sample-app:sha-abc123"

	runPipeline(t, appRepo, gitopsRepo, "sample-app", "staging", image)
	before := git(t, gitopsRepo, "rev-parse", "HEAD")

	_, _, _, second := runPipeline(t, appRepo, gitopsRepo, "sample-app", "staging", image)
	if second.Committed {
		t.Error("the second identical run created a commit")
	}
	if after := git(t, gitopsRepo, "rev-parse", "HEAD"); after != before {
		t.Error("HEAD moved on an unchanged re-run")
	}
}

func TestPipeline_NewImageTagProducesExactlyOneCommit(t *testing.T) {
	appRepo := sampleAppCopy(t)
	gitopsRepo := newGitopsRepo(t)

	runPipeline(t, appRepo, gitopsRepo, "sample-app", "staging", "registry.example.com/sample-app:sha-aaa")
	runPipeline(t, appRepo, gitopsRepo, "sample-app", "staging", "registry.example.com/sample-app:sha-bbb")

	if count := git(t, gitopsRepo, "rev-list", "--count", "HEAD"); count != "3" {
		t.Errorf("commit count = %s, want 3 (init + two deploys)", count)
	}

	deployment, err := os.ReadFile(filepath.Join(gitopsRepo, "apps", "sample-app", "staging", "deployment.yaml"))
	if err != nil {
		t.Fatalf("reading deployment.yaml: %v", err)
	}
	if !strings.Contains(string(deployment), "sha-bbb") {
		t.Error("the committed Deployment does not reference the newest image tag")
	}

	// The diff between deploys should be the image line and nothing else.
	diff := git(t, gitopsRepo, "diff", "HEAD~1", "HEAD", "--", "apps/sample-app/staging/deployment.yaml")
	if strings.Count(diff, "\n+") > 3 {
		t.Errorf("an image bump should be a one-line diff, got:\n%s", diff)
	}
}

// Two environments are two directories under one repo, with no shared
// state. Deploying to staging must not touch production.
func TestPipeline_EnvironmentsAreIsolated(t *testing.T) {
	appRepo := sampleAppCopy(t)
	gitopsRepo := newGitopsRepo(t)

	runPipeline(t, appRepo, gitopsRepo, "sample-app", "staging", "registry.example.com/sample-app:sha-aaa")
	prodBefore := filepath.Join(gitopsRepo, "apps", "sample-app", "production")
	if _, err := os.Stat(prodBefore); !os.IsNotExist(err) {
		t.Fatal("a staging deploy created a production directory")
	}

	runPipeline(t, appRepo, gitopsRepo, "sample-app", "production", "registry.example.com/sample-app:sha-bbb")

	staging, err := os.ReadFile(filepath.Join(gitopsRepo, "apps", "sample-app", "staging", "deployment.yaml"))
	if err != nil {
		t.Fatalf("reading staging deployment: %v", err)
	}
	if !strings.Contains(string(staging), "sha-aaa") {
		t.Error("the production deploy overwrote staging's image")
	}

	ingress, err := os.ReadFile(filepath.Join(gitopsRepo, "apps", "sample-app", "production", "ingress.yaml"))
	if err != nil {
		t.Fatalf("reading production ingress: %v", err)
	}
	if !strings.Contains(string(ingress), "sample-app.production.example.com") {
		t.Error("the production Ingress host does not carry the production env")
	}
}

// The self-healing path: a deploy goes out, the pods crash-loop,
// healthwatcher's deterministic rules call it unhealthy, and the rollback
// is a git revert that restores the previously working manifest.
func TestPipeline_UnhealthyRolloutRollsBackToTheLastGoodManifest(t *testing.T) {
	appRepo := sampleAppCopy(t)
	gitopsRepo := newGitopsRepo(t)

	runPipeline(t, appRepo, gitopsRepo, "sample-app", "staging", "registry.example.com/sample-app:sha-good")
	good := git(t, gitopsRepo, "rev-parse", "HEAD")

	runPipeline(t, appRepo, gitopsRepo, "sample-app", "staging", "registry.example.com/sample-app:sha-broken")

	health := healthwatcher.Evaluate([]healthwatcher.PodStatus{
		{Name: "sample-app-7d9f", Phase: "Running", RestartCount: 6, WaitingReason: "CrashLoopBackOff"},
	})
	if health.Healthy {
		t.Fatal("a crash-looping pod was evaluated as healthy")
	}

	explanation, fix := healthwatcher.RootCause(context.Background(), nil, "sample-app", health, "")
	if explanation == "" || fix == "" {
		t.Error("the no-AI fallback must still produce an explanation and a next step")
	}

	if _, err := gitopswriter.RevertLastCommit(gitopswriter.RevertRequest{
		GitopsRepoPath: gitopsRepo,
		CommitMessage:  "rollback(sample-app/staging): " + health.Reason,
	}); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	deployment, err := os.ReadFile(filepath.Join(gitopsRepo, "apps", "sample-app", "staging", "deployment.yaml"))
	if err != nil {
		t.Fatalf("reading deployment.yaml after rollback: %v", err)
	}
	if !strings.Contains(string(deployment), "sha-good") {
		t.Error("after rollback the manifest does not reference the last known good image")
	}
	if diff := git(t, gitopsRepo, "diff", "--stat", good, "HEAD"); diff != "" {
		t.Errorf("tree after rollback differs from the last good commit:\n%s", diff)
	}

	// A revert, not a reset: the bad deploy stays in history so the
	// incident is still reconstructable afterwards.
	if !strings.Contains(git(t, gitopsRepo, "log", "--oneline"), "rollback(sample-app/staging)") {
		t.Error("the rollback commit is missing from history")
	}
	if count := git(t, gitopsRepo, "rev-list", "--count", "HEAD"); count != "4" {
		t.Errorf("commit count = %s, want 4 — the bad deploy must remain in history", count)
	}
}

// A risky manifest must be blocked before it is ever committed. This is the
// ordering cmd/risk-reviewer enforces in CI, verified against the writer's
// dry-run mode.
func TestPipeline_RiskyManifestIsBlockedBeforeCommit(t *testing.T) {
	gitopsRepo := newGitopsRepo(t)
	before := git(t, gitopsRepo, "rev-parse", "HEAD")

	risky := manifest.Output{
		Deployment: "apiVersion: apps/v1\nkind: Deployment\nspec:\n  replicas: 0\n  template:\n    spec:\n      containers:\n        - securityContext:\n            privileged: true\n",
		Service:    "kind: Service\n",
	}

	review := riskreview.Run(context.Background(), nil, "sample-app", risky.Deployment, "")
	if review.Approved {
		t.Fatal("a zero-replica privileged Deployment was approved")
	}

	// Gate held: nothing is committed.
	if _, err := gitopswriter.Write(gitopswriter.WriteRequest{
		GitopsRepoPath: gitopsRepo, AppName: "sample-app", Env: "staging",
		Manifests: risky, DryRun: true,
	}); err != nil {
		t.Fatalf("dry-run write: %v", err)
	}
	if after := git(t, gitopsRepo, "rev-parse", "HEAD"); after != before {
		t.Error("a blocked deploy still moved HEAD")
	}
}
