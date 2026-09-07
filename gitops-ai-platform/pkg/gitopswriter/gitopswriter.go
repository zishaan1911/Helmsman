// Package gitopswriter is the boundary between "AI-generated stuff" and
// "the cluster". It never talks to Kubernetes directly — it only writes
// files into a GitOps repo and commits them. ArgoCD/Flux, running
// unmodified, is what actually applies changes to the cluster. This keeps
// every change auditable (it's a Git commit), revertible (git revert), and
// gated by whatever branch-protection/PR rules the org already uses.
package gitopswriter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/manifest"
)

// WriteRequest describes what to commit and where.
type WriteRequest struct {
	GitopsRepoPath string // local path to an already-cloned GitOps repo
	AppName        string
	Env            string // "staging", "production", etc.
	Manifests      manifest.Output
	CommitMessage  string
	Push           bool // if true, push after commit (requires configured remote/credentials)
	DryRun         bool // if true, write files but do not touch git at all
}

// WriteResult reports what happened.
type WriteResult struct {
	AppDir       string
	FilesWritten []string
	CommitSHA    string
	Committed    bool
	Pushed       bool
}

// Write lays down the manifest files under
// apps/<appName>/<env>/{deployment,service,ingress,kustomization}.yaml
// and, unless DryRun, commits (and optionally pushes) the change.
func Write(req WriteRequest) (WriteResult, error) {
	if req.GitopsRepoPath == "" {
		return WriteResult{}, fmt.Errorf("GitopsRepoPath is required")
	}
	if req.AppName == "" || req.Env == "" {
		return WriteResult{}, fmt.Errorf("AppName and Env are required")
	}

	appDir := filepath.Join(req.GitopsRepoPath, "apps", req.AppName, req.Env)
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		return WriteResult{}, fmt.Errorf("creating %s: %w", appDir, err)
	}

	result := WriteResult{AppDir: appDir}

	files := map[string]string{
		"deployment.yaml":    req.Manifests.Deployment,
		"service.yaml":       req.Manifests.Service,
		"kustomization.yaml": req.Manifests.Kustomization,
	}
	if req.Manifests.Ingress != "" {
		files["ingress.yaml"] = req.Manifests.Ingress
	}

	// Deterministic write order so commits/diffs are stable.
	for _, name := range []string{"deployment.yaml", "service.yaml", "ingress.yaml", "kustomization.yaml"} {
		content, ok := files[name]
		if !ok {
			continue
		}
		path := filepath.Join(appDir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return result, fmt.Errorf("writing %s: %w", path, err)
		}
		result.FilesWritten = append(result.FilesWritten, path)
	}

	if req.DryRun {
		return result, nil
	}

	msg := req.CommitMessage
	if msg == "" {
		msg = fmt.Sprintf("chore(%s/%s): update manifests via gitops-ai-platform [%s]",
			req.AppName, req.Env, time.Now().UTC().Format(time.RFC3339))
	}

	if err := runGit(req.GitopsRepoPath, "add", "apps"); err != nil {
		return result, fmt.Errorf("git add: %w", err)
	}

	// Nothing to commit is not an error — the pipeline may run when
	// generated manifests are unchanged from last time.
	if clean, err := isClean(req.GitopsRepoPath); err != nil {
		return result, err
	} else if clean {
		return result, nil
	}

	if err := runGit(req.GitopsRepoPath, "commit", "-m", msg); err != nil {
		return result, fmt.Errorf("git commit: %w", err)
	}
	result.Committed = true

	sha, err := gitOutput(req.GitopsRepoPath, "rev-parse", "HEAD")
	if err != nil {
		return result, fmt.Errorf("git rev-parse: %w", err)
	}
	result.CommitSHA = strings.TrimSpace(sha)

	if req.Push {
		if err := runGit(req.GitopsRepoPath, "push"); err != nil {
			return result, fmt.Errorf("git push: %w", err)
		}
		result.Pushed = true
	}

	return result, nil
}

// isClean reports whether the index has no staged changes relative to HEAD
// (i.e. `git add` picked up nothing new — the generated manifests are
// byte-identical to what's already committed).
func isClean(repoPath string) (bool, error) {
	cmd := exec.Command("git", "diff", "--cached", "--quiet")
	cmd.Dir = repoPath
	err := cmd.Run()
	if err == nil {
		return true, nil // exit 0 => no staged diff
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return false, nil // exit 1 => there is a staged diff
	}
	return false, fmt.Errorf("git diff --cached: %w", err)
}

func runGit(repoPath string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func gitOutput(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	return string(out), err
}

// RevertRequest describes a rollback: revert the most recent commit(s)
// touching an app's manifests in the GitOps repo. This is how
// healthwatcher rolls back an unhealthy deploy — never by mutating the
// cluster directly, always by committing the inverse change and letting
// ArgoCD/Flux reconcile it, same as any other change.
type RevertRequest struct {
	GitopsRepoPath string
	CommitMessage  string
	Push           bool
}

// RevertLastCommit reverts HEAD in the GitOps repo (git revert --no-edit)
// and optionally pushes. Returns the new commit SHA (the revert commit).
func RevertLastCommit(req RevertRequest) (WriteResult, error) {
	if req.GitopsRepoPath == "" {
		return WriteResult{}, fmt.Errorf("GitopsRepoPath is required")
	}

	args := []string{"revert", "--no-edit", "HEAD"}
	if req.CommitMessage != "" {
		args = []string{"revert", "--no-edit", "-m", req.CommitMessage, "HEAD"}
	}
	if err := runGit(req.GitopsRepoPath, args...); err != nil {
		return WriteResult{}, fmt.Errorf("git revert: %w", err)
	}

	sha, err := gitOutput(req.GitopsRepoPath, "rev-parse", "HEAD")
	if err != nil {
		return WriteResult{}, fmt.Errorf("git rev-parse: %w", err)
	}
	result := WriteResult{Committed: true, CommitSHA: strings.TrimSpace(sha)}

	if req.Push {
		if err := runGit(req.GitopsRepoPath, "push"); err != nil {
			return result, fmt.Errorf("git push: %w", err)
		}
		result.Pushed = true
	}
	return result, nil
}

// ArgoApplication renders an ArgoCD Application resource pointing at the
// path this package just wrote to. This is typically committed once per
// app (not on every deploy) — callers generate it the first time an app
// onboards onto the platform.
func ArgoApplication(appName, env, gitopsRepoURL, targetRevision, destNamespace string) string {
	return fmt.Sprintf(`apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: %s-%s
  namespace: argocd
  labels:
    managed-by: gitops-ai-platform
spec:
  project: default
  source:
    repoURL: %s
    targetRevision: %s
    path: apps/%s/%s
  destination:
    server: https://kubernetes.default.svc
    namespace: %s
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
`, appName, env, gitopsRepoURL, targetRevision, appName, env, destNamespace)
}
