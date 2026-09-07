package gitopswriter

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/manifest"
)

// newGitopsRepo creates a throwaway Git repo standing in for the real
// ArgoCD-watched one. Identity is set on the repo rather than globally so
// the test never depends on (or disturbs) the developer's git config.
func newGitopsRepo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}

	dir := t.TempDir()
	mustGit(t, dir, "init", "-q", "-b", "main")
	mustGit(t, dir, "config", "user.email", "platform@example.com")
	mustGit(t, dir, "config", "user.name", "gitops-ai-platform")
	mustGit(t, dir, "config", "commit.gpgsign", "false")
	mustGit(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func sampleManifests() manifest.Output {
	return manifest.Output{
		Deployment:    "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: sample-app\n",
		Service:       "apiVersion: v1\nkind: Service\nmetadata:\n  name: sample-app\n",
		Kustomization: "resources:\n  - deployment.yaml\n  - service.yaml\n",
	}
}

func TestWrite_CommitsManifestsToTheExpectedPath(t *testing.T) {
	repo := newGitopsRepo(t)

	res, err := Write(WriteRequest{
		GitopsRepoPath: repo,
		AppName:        "sample-app",
		Env:            "staging",
		Manifests:      sampleManifests(),
		CommitMessage:  "deploy sample-app to staging",
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	wantDir := filepath.Join(repo, "apps", "sample-app", "staging")
	if res.AppDir != wantDir {
		t.Errorf("AppDir = %q, want %q", res.AppDir, wantDir)
	}
	if !res.Committed {
		t.Error("Committed = false, want true")
	}
	if res.CommitSHA == "" {
		t.Error("CommitSHA is empty for a successful commit")
	}
	if res.Pushed {
		t.Error("Pushed = true, but Push was not requested")
	}

	for _, name := range []string{"deployment.yaml", "service.yaml", "kustomization.yaml"} {
		if _, err := os.Stat(filepath.Join(wantDir, name)); err != nil {
			t.Errorf("expected %s on disk: %v", name, err)
		}
	}

	if subject := mustGit(t, repo, "log", "-1", "--pretty=%s"); subject != "deploy sample-app to staging" {
		t.Errorf("commit subject = %q, want the supplied CommitMessage", subject)
	}

	// The commit must be the whole change: a dirty tree afterwards means
	// ArgoCD would sync a half-written state.
	if status := mustGit(t, repo, "status", "--porcelain"); status != "" {
		t.Errorf("working tree is dirty after Write():\n%s", status)
	}
}

func TestWrite_IngressOnlyWhenProvided(t *testing.T) {
	repo := newGitopsRepo(t)

	m := sampleManifests()
	if _, err := Write(WriteRequest{GitopsRepoPath: repo, AppName: "private-app", Env: "prod", Manifests: m}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "apps", "private-app", "prod", "ingress.yaml")); !os.IsNotExist(err) {
		t.Error("ingress.yaml was written even though Manifests.Ingress was empty")
	}

	m.Ingress = "apiVersion: networking.k8s.io/v1\nkind: Ingress\n"
	res, err := Write(WriteRequest{GitopsRepoPath: repo, AppName: "public-app", Env: "prod", Manifests: m})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "apps", "public-app", "prod", "ingress.yaml")); err != nil {
		t.Errorf("expected ingress.yaml when Manifests.Ingress is set: %v", err)
	}
	if len(res.FilesWritten) != 4 {
		t.Errorf("len(FilesWritten) = %d, want 4", len(res.FilesWritten))
	}
}

// The write order is fixed so that re-running the pipeline produces
// byte-identical trees and therefore reviewable diffs.
func TestWrite_FilesWrittenInDeterministicOrder(t *testing.T) {
	repo := newGitopsRepo(t)
	m := sampleManifests()
	m.Ingress = "kind: Ingress\n"

	res, err := Write(WriteRequest{GitopsRepoPath: repo, AppName: "app", Env: "staging", Manifests: m})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	want := []string{"deployment.yaml", "service.yaml", "ingress.yaml", "kustomization.yaml"}
	if len(res.FilesWritten) != len(want) {
		t.Fatalf("FilesWritten = %v, want %d entries", res.FilesWritten, len(want))
	}
	for i, name := range want {
		if filepath.Base(res.FilesWritten[i]) != name {
			t.Errorf("FilesWritten[%d] = %q, want %q", i, filepath.Base(res.FilesWritten[i]), name)
		}
	}
}

// The pipeline runs on every push, including pushes that don't change the
// generated output. Committing anyway would fill the GitOps repo with empty
// commits and make ArgoCD's history useless for auditing.
func TestWrite_UnchangedManifestsProduceNoCommit(t *testing.T) {
	repo := newGitopsRepo(t)
	req := WriteRequest{GitopsRepoPath: repo, AppName: "app", Env: "staging", Manifests: sampleManifests()}

	first, err := Write(req)
	if err != nil {
		t.Fatalf("first Write() error = %v", err)
	}
	if !first.Committed {
		t.Fatal("first Write() should have committed")
	}

	second, err := Write(req)
	if err != nil {
		t.Fatalf("second Write() error = %v, want nil — an unchanged run is not a failure", err)
	}
	if second.Committed {
		t.Error("Committed = true on a no-op run; expected no empty commit")
	}
	if second.CommitSHA != "" {
		t.Errorf("CommitSHA = %q, want empty when nothing was committed", second.CommitSHA)
	}

	if count := mustGit(t, repo, "rev-list", "--count", "HEAD"); count != "2" {
		t.Errorf("commit count = %s, want 2 (init + one manifest commit)", count)
	}
}

func TestWrite_ChangedManifestsProduceASecondCommit(t *testing.T) {
	repo := newGitopsRepo(t)
	req := WriteRequest{GitopsRepoPath: repo, AppName: "app", Env: "staging", Manifests: sampleManifests()}

	if _, err := Write(req); err != nil {
		t.Fatalf("first Write() error = %v", err)
	}

	req.Manifests.Deployment = strings.Replace(req.Manifests.Deployment, "apps/v1", "apps/v1 # bumped", 1)
	second, err := Write(req)
	if err != nil {
		t.Fatalf("second Write() error = %v", err)
	}
	if !second.Committed {
		t.Error("Committed = false, want true when the manifests actually changed")
	}
}

func TestWrite_DryRunTouchesNoGitState(t *testing.T) {
	repo := newGitopsRepo(t)
	before := mustGit(t, repo, "rev-parse", "HEAD")

	res, err := Write(WriteRequest{
		GitopsRepoPath: repo, AppName: "app", Env: "staging",
		Manifests: sampleManifests(), DryRun: true,
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if res.Committed {
		t.Error("Committed = true during a dry run")
	}
	if len(res.FilesWritten) == 0 {
		t.Error("a dry run should still render the files so they can be inspected")
	}
	if after := mustGit(t, repo, "rev-parse", "HEAD"); after != before {
		t.Error("HEAD moved during a dry run")
	}
	if status := mustGit(t, repo, "status", "--porcelain"); status == "" {
		t.Error("expected the dry-run files to be present but uncommitted")
	}
}

func TestWrite_GeneratesADefaultCommitMessage(t *testing.T) {
	repo := newGitopsRepo(t)

	if _, err := Write(WriteRequest{
		GitopsRepoPath: repo, AppName: "billing", Env: "production",
		Manifests: sampleManifests(),
	}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	subject := mustGit(t, repo, "log", "-1", "--pretty=%s")
	for _, want := range []string{"billing", "production"} {
		if !strings.Contains(subject, want) {
			t.Errorf("default commit subject %q should identify the app and env (missing %q)", subject, want)
		}
	}
}

func TestWrite_RequiredFields(t *testing.T) {
	cases := []struct {
		name string
		req  WriteRequest
	}{
		{"no repo path", WriteRequest{AppName: "app", Env: "staging"}},
		{"no app name", WriteRequest{GitopsRepoPath: t.TempDir(), Env: "staging"}},
		{"no env", WriteRequest{GitopsRepoPath: t.TempDir(), AppName: "app"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Write(tc.req); err == nil {
				t.Fatal("Write() = nil error, want a validation error")
			}
		})
	}
}

// Rollback is the platform's most consequential automated action, and it
// happens while something is already broken. It has to work.
func TestRevertLastCommit_RestoresThePreviousState(t *testing.T) {
	repo := newGitopsRepo(t)
	req := WriteRequest{GitopsRepoPath: repo, AppName: "app", Env: "staging", Manifests: sampleManifests()}

	if _, err := Write(req); err != nil {
		t.Fatalf("first Write() error = %v", err)
	}
	good := mustGit(t, repo, "rev-parse", "HEAD")

	req.Manifests.Deployment = "apiVersion: apps/v1\nkind: Deployment\nspec:\n  replicas: 0\n"
	if _, err := Write(req); err != nil {
		t.Fatalf("second Write() error = %v", err)
	}

	res, err := RevertLastCommit(RevertRequest{GitopsRepoPath: repo})
	if err != nil {
		t.Fatalf("RevertLastCommit() error = %v", err)
	}
	if !res.Committed || res.CommitSHA == "" {
		t.Errorf("expected a revert commit, got %+v", res)
	}

	// A revert, not a reset: history is preserved and the tree matches the
	// last-known-good commit.
	if diff := mustGit(t, repo, "diff", "--stat", good, "HEAD"); diff != "" {
		t.Errorf("tree after revert differs from the last good commit:\n%s", diff)
	}
	if count := mustGit(t, repo, "rev-list", "--count", "HEAD"); count != "4" {
		t.Errorf("commit count = %s, want 4 — the bad commit must stay in history", count)
	}
}

// Regression test: RevertLastCommit used to pass CommitMessage to
// `git revert -m`, where -m means the mainline parent number. git rejects a
// non-numeric argument, so every rollback that set a message failed.
func TestRevertLastCommit_CustomMessageIsUsedAsTheSubject(t *testing.T) {
	repo := newGitopsRepo(t)

	if _, err := Write(WriteRequest{
		GitopsRepoPath: repo, AppName: "app", Env: "staging", Manifests: sampleManifests(),
	}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	const msg = "rollback(app/staging): CrashLoopBackOff after deploy"
	res, err := RevertLastCommit(RevertRequest{GitopsRepoPath: repo, CommitMessage: msg})
	if err != nil {
		t.Fatalf("RevertLastCommit() error = %v", err)
	}

	if subject := mustGit(t, repo, "log", "-1", "--pretty=%s"); subject != msg {
		t.Errorf("commit subject = %q, want %q", subject, msg)
	}
	if head := mustGit(t, repo, "rev-parse", "HEAD"); head != res.CommitSHA {
		t.Errorf("CommitSHA = %q, want HEAD %q", res.CommitSHA, head)
	}
	if status := mustGit(t, repo, "status", "--porcelain"); status != "" {
		t.Errorf("revert left the tree dirty:\n%s", status)
	}
	// The sequencer must be clear, or the next revert refuses to start.
	if _, err := os.Stat(filepath.Join(repo, ".git", "sequencer")); !os.IsNotExist(err) {
		t.Error("revert sequencer state was left behind")
	}
}

func TestRevertLastCommit_RequiresARepoPath(t *testing.T) {
	if _, err := RevertLastCommit(RevertRequest{}); err == nil {
		t.Fatal("RevertLastCommit() = nil error, want a validation error")
	}
}

func TestRevertLastCommit_SurfacesGitFailures(t *testing.T) {
	// A repo with only the root commit and no parent to revert onto.
	dir := t.TempDir()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	mustGit(t, dir, "init", "-q", "-b", "main")

	_, err := RevertLastCommit(RevertRequest{GitopsRepoPath: dir})
	if err == nil {
		t.Fatal("RevertLastCommit() = nil error, want an error when there is nothing to revert")
	}
	if !strings.Contains(err.Error(), "git") {
		t.Errorf("error %q should name the failing git command", err)
	}
}

func TestArgoApplication_RendersAValidOnboardingResource(t *testing.T) {
	got := ArgoApplication("sample-app", "staging", "https://github.com/you/gitops-repo.git", "main", "sample-app")

	for _, want := range []string{
		"apiVersion: argoproj.io/v1alpha1",
		"kind: Application",
		"name: sample-app-staging",
		"namespace: argocd",
		"repoURL: https://github.com/you/gitops-repo.git",
		"targetRevision: main",
		"path: apps/sample-app/staging",
		"namespace: sample-app",
		"managed-by: gitops-ai-platform",
		"CreateNamespace=true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered Application is missing %q:\n%s", want, got)
		}
	}

	// The path must match where Write() actually puts the files, or ArgoCD
	// syncs an empty directory and prunes the app out of the cluster.
	repo := newGitopsRepo(t)
	res, err := Write(WriteRequest{
		GitopsRepoPath: repo, AppName: "sample-app", Env: "staging", Manifests: sampleManifests(),
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	rel, err := filepath.Rel(repo, res.AppDir)
	if err != nil {
		t.Fatalf("computing relative path: %v", err)
	}
	if !strings.Contains(got, "path: "+filepath.ToSlash(rel)) {
		t.Errorf("Application path does not match where Write() wrote files (%s):\n%s", rel, got)
	}
}
