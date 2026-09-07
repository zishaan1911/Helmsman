// Package healthwatcher checks rollout health after ArgoCD/Flux has
// synced a change, and — if the rollout is unhealthy — gathers evidence,
// asks Gemini for a plain-English root cause, and can trigger a rollback.
//
// This package never calls the Kubernetes API directly (no k8s.io/client-go
// dependency): it shells out to `kubectl`, which is already configured
// with whatever cluster credentials the environment provides (a CI runner,
// a CronJob's ServiceAccount, etc). This keeps the module dependency-free
// and matches how most platform teams already grant scoped cluster access.
//
// Rollback is implemented as a `git revert` in the GitOps repo, not a
// direct `kubectl rollback` — consistent with this platform's rule that
// nothing touches the cluster except ArgoCD/Flux reconciling from Git.
package healthwatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/gemini"
)

// PodStatus is the minimal subset of `kubectl get pods -o json` this
// package cares about — deliberately not the full k8s.io/api Pod type, to
// avoid pulling in that dependency for a handful of fields.
type PodStatus struct {
	Name          string
	Phase         string
	RestartCount  int
	WaitingReason string // e.g. "CrashLoopBackOff", "ImagePullBackOff"
}

// HealthResult is the outcome of a health check.
type HealthResult struct {
	Healthy bool
	Reason  string
	Pods    []PodStatus
}

// unhealthyRestartThreshold: a pod with more restarts than this in the
// observation window is considered unhealthy even without an explicit
// waiting-reason signal.
const unhealthyRestartThreshold = 3

// RolloutStatus blocks until `kubectl rollout status` reports success or
// the timeout elapses, mirroring how a human would watch a rollout.
func RolloutStatus(namespace, deployment string, timeoutSeconds int) error {
	cmd := exec.Command("kubectl", "rollout", "status",
		fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace,
		"--timeout", fmt.Sprintf("%ds", timeoutSeconds),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rollout did not become healthy within %ds: %w\n%s", timeoutSeconds, err, string(out))
	}
	return nil
}

// kubectlPodList is the subset of `kubectl get pods -o json` output shape
// this package reads.
type kubectlPodList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			Phase             string `json:"phase"`
			ContainerStatuses []struct {
				RestartCount int `json:"restartCount"`
				State        struct {
					Waiting *struct {
						Reason string `json:"reason"`
					} `json:"waiting"`
				} `json:"state"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

// GetPodStatuses shells to kubectl and returns the current pod statuses
// for the given label selector (typically "app=<appName>").
func GetPodStatuses(namespace, labelSelector string) ([]PodStatus, error) {
	cmd := exec.Command("kubectl", "get", "pods", "-n", namespace, "-l", labelSelector, "-o", "json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl get pods: %w", err)
	}
	return parsePodListJSON(out)
}

// parsePodListJSON is factored out from GetPodStatuses so the parsing
// logic can be unit tested without a live cluster or kubectl binary.
func parsePodListJSON(data []byte) ([]PodStatus, error) {
	var list kubectlPodList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parsing kubectl output: %w", err)
	}

	statuses := make([]PodStatus, 0, len(list.Items))
	for _, item := range list.Items {
		ps := PodStatus{Name: item.Metadata.Name, Phase: item.Status.Phase}
		for _, cs := range item.Status.ContainerStatuses {
			if cs.RestartCount > ps.RestartCount {
				ps.RestartCount = cs.RestartCount
			}
			if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
				ps.WaitingReason = cs.State.Waiting.Reason
			}
		}
		statuses = append(statuses, ps)
	}
	return statuses, nil
}

// Evaluate applies deterministic health rules to a set of pod statuses.
// No AI here — "is this pod crash-looping" is not a judgment call.
func Evaluate(pods []PodStatus) HealthResult {
	for _, p := range pods {
		if p.WaitingReason == "CrashLoopBackOff" || p.WaitingReason == "ImagePullBackOff" || p.WaitingReason == "ErrImagePull" {
			return HealthResult{Healthy: false, Pods: pods,
				Reason: fmt.Sprintf("pod %s is in %s", p.Name, p.WaitingReason)}
		}
		if p.Phase == "Failed" {
			return HealthResult{Healthy: false, Pods: pods,
				Reason: fmt.Sprintf("pod %s is in phase Failed", p.Name)}
		}
		if p.RestartCount > unhealthyRestartThreshold {
			return HealthResult{Healthy: false, Pods: pods,
				Reason: fmt.Sprintf("pod %s has restarted %d times", p.Name, p.RestartCount)}
		}
	}
	if len(pods) == 0 {
		return HealthResult{Healthy: false, Pods: pods, Reason: "no pods found matching the label selector"}
	}
	return HealthResult{Healthy: true, Pods: pods, Reason: "all pods healthy"}
}

// FetchRecentEvents returns recent K8s events for the namespace, truncated
// to keep the Gemini prompt small. Best-effort: an error here shouldn't
// block root-cause analysis, just makes it less informed.
func FetchRecentEvents(namespace string) string {
	cmd := exec.Command("kubectl", "get", "events", "-n", namespace,
		"--sort-by", ".lastTimestamp", "--field-selector", "type=Warning")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) > 30 {
		lines = lines[len(lines)-30:]
	}
	return strings.Join(lines, "\n")
}

const rootCauseSystemPrompt = `You are diagnosing a failed Kubernetes deployment rollout for a GitOps platform.
You will receive the app name, the deployment health check's reason, pod statuses, and recent Warning-type K8s events.
Explain the likely root cause in plain English for a developer who may not be a Kubernetes expert, and suggest one concrete next step.
Respond ONLY with JSON matching exactly this shape: {"explanation": "<2-4 sentences>", "suggestedFix": "<1-2 sentences>"}`

type rootCauseResponse struct {
	Explanation  string `json:"explanation"`
	SuggestedFix string `json:"suggestedFix"`
}

// RootCause asks Gemini to explain an unhealthy rollout in plain English.
// If geminiClient is nil or the call fails, returns a templated fallback
// built from the raw signals instead of leaving the caller with nothing —
// an on-call engineer should never get a blank issue body.
func RootCause(ctx context.Context, geminiClient *gemini.Client, appName string, health HealthResult, events string) (explanation, suggestedFix string) {
	if geminiClient != nil {
		userPrompt := fmt.Sprintf("App: %s\nHealth check reason: %s\nPod statuses: %+v\nRecent warning events:\n%s",
			appName, health.Reason, health.Pods, events)
		var resp rootCauseResponse
		if err := geminiClient.GenerateJSON(ctx, rootCauseSystemPrompt, userPrompt, &resp); err == nil {
			return resp.Explanation, resp.SuggestedFix
		}
	}
	return fmt.Sprintf("Automated diagnosis unavailable. Raw signal: %s.", health.Reason),
		"Check `kubectl describe pod` and recent events for this app manually."
}
