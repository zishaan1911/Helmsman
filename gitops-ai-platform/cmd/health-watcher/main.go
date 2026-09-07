// Command health-watcher watches a Deployment's rollout after ArgoCD/Flux
// has synced it. On failure, it gathers pod status + events, asks Gemini
// for a plain-English root cause, and — if -auto-rollback is set — reverts
// the last commit in the GitOps repo so the next sync restores the
// previous known-good state.
//
// Intended to run as a Kubernetes CronJob/Job (see deploy/manifests) with
// a ServiceAccount scoped to read pods/events and kubectl configured via
// in-cluster config, or as an ArgoCD PostSync hook.
//
// Usage:
//
//	health-watcher -namespace myapp -app myapp -deployment myapp \
//	  -gitops-repo /path/to/cloned/gitops/repo -auto-rollback -push
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/internal/version"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/gemini"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/gitopswriter"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/healthwatcher"
)

func main() {
	namespace := flag.String("namespace", "", "K8s namespace (required)")
	appName := flag.String("app", "", "application name / label selector value for app= (required)")
	deployment := flag.String("deployment", "", "Deployment name to watch (defaults to -app)")
	timeout := flag.Int("timeout", 180, "seconds to wait for rollout to become healthy")
	gitopsRepo := flag.String("gitops-repo", "", "path to a locally cloned GitOps repo (required if -auto-rollback)")
	autoRollback := flag.Bool("auto-rollback", false, "revert the last GitOps commit if the rollout is unhealthy")
	push := flag.Bool("push", false, "push the revert commit after creating it")
	showVersion := version.Flag()
	flag.Parse()
	version.Exit(*showVersion, "health-watcher")

	if *namespace == "" || *appName == "" {
		fmt.Fprintln(os.Stderr, "health-watcher: -namespace and -app are required")
		os.Exit(2)
	}
	if *deployment == "" {
		*deployment = *appName
	}

	ctx := context.Background()
	var client *gemini.Client
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		client = gemini.New(key)
	}

	fmt.Printf("watching rollout of deployment/%s in namespace %s (timeout %ds)...\n", *deployment, *namespace, *timeout)
	rolloutErr := healthwatcher.RolloutStatus(*namespace, *deployment, *timeout)

	pods, err := healthwatcher.GetPodStatuses(*namespace, "app="+*appName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "health-watcher: fetching pod statuses:", err)
		// Don't exit — we may still have useful info from rolloutErr alone.
	}
	health := healthwatcher.Evaluate(pods)

	if rolloutErr == nil && health.Healthy {
		fmt.Println("✓ rollout healthy")
		return
	}

	fmt.Println("✗ rollout unhealthy")
	if rolloutErr != nil {
		fmt.Println("  rollout status error:", rolloutErr)
	}
	fmt.Println("  reason:", health.Reason)

	events := healthwatcher.FetchRecentEvents(*namespace)
	explanation, fix := healthwatcher.RootCause(ctx, client, *appName, health, events)
	fmt.Println()
	fmt.Println("Root cause analysis:")
	fmt.Println(" ", explanation)
	fmt.Println("Suggested fix:")
	fmt.Println(" ", fix)

	if *autoRollback {
		if *gitopsRepo == "" {
			fmt.Fprintln(os.Stderr, "health-watcher: -auto-rollback requires -gitops-repo")
			os.Exit(1)
		}
		fmt.Println()
		fmt.Println("Rolling back: reverting last commit in GitOps repo...")
		result, err := gitopswriter.RevertLastCommit(gitopswriter.RevertRequest{
			GitopsRepoPath: *gitopsRepo,
			CommitMessage:  fmt.Sprintf("revert: rollback %s — %s", *appName, health.Reason),
			Push:           *push,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "health-watcher: rollback failed:", err)
			os.Exit(1)
		}
		fmt.Printf("reverted, new commit %s (pushed=%v)\n", result.CommitSHA, result.Pushed)
		fmt.Println("ArgoCD/Flux will reconcile the cluster back to the previous known-good state.")
	}

	os.Exit(1)
}
