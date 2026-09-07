// Package riskreview scores a proposed manifest change for risk before it
// lands in the GitOps repo. It deliberately splits the work into two
// layers:
//
//  1. Static checks — deterministic, fast, no AI, no network call. These
//     catch the things that are always wrong regardless of context
//     (missing resource limits, zero replicas, running as root). Treat
//     this layer the same way you'd treat OPA/Kyverno policy — in a real
//     deployment, replace/augment it with actual OPA/Kyverno rather than
//     hand-rolled string checks; the interface here is what matters.
//
//  2. AI review — judgment calls that need context a static rule can't
//     encode: is this replica jump reasonable given the diff, does this
//     newly-added env var look like a secret that should be a secretRef,
//     does this look like a bigger blast-radius change than usual for
//     this app. This is where Gemini earns its keep.
//
// The AI layer is only ever additive to findings — it cannot silence a
// static finding. If Gemini is unreachable or returns something
// unparseable, review fails closed (falls back to static-only findings,
// never "pass with no review").
package riskreview

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/gemini"
)

type Severity string

const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// Finding is a single issue surfaced by either the static or AI layer.
type Finding struct {
	Severity Severity `json:"severity"`
	Rule     string   `json:"rule"`
	Message  string   `json:"message"`
	Source   string   `json:"source"` // "static" or "ai"
}

// Review is the final result: everything a human (or an auto-merge gate)
// needs to decide whether this deploy should proceed.
type Review struct {
	RiskScore  int       `json:"riskScore"` // 0-100
	Findings   []Finding `json:"findings"`
	Summary    string    `json:"summary"`
	Approved   bool      `json:"approved"`   // true if riskScore is below the configured threshold
	AIReviewed bool      `json:"aiReviewed"` // false if the AI layer was skipped or failed
}

// ApprovalThreshold: risk scores at or above this block auto-merge and
// require a human look. Kept as a var (not const) so callers/CI can tune it.
var ApprovalThreshold = 60

// staticChecks runs deterministic rules against the newly-generated
// Deployment manifest. It only knows about the exact shape pkg/manifest
// produces (see pkg/manifest/manifest.go) — this is not a general K8s
// linter, intentionally, to keep it dependency-free and precise.
func staticChecks(newDeployment, oldDeployment string) []Finding {
	var findings []Finding

	if !strings.Contains(newDeployment, "limits:") {
		findings = append(findings, Finding{
			Severity: SeverityHigh, Rule: "resource-limits-required", Source: "static",
			Message: "Deployment has no resource limits set — a runaway process could starve the node.",
		})
	}

	if m := regexp.MustCompile(`replicas:\s*(\d+)`).FindStringSubmatch(newDeployment); len(m) == 2 {
		if n, _ := strconv.Atoi(m[1]); n == 0 {
			findings = append(findings, Finding{
				Severity: SeverityCritical, Rule: "zero-replicas", Source: "static",
				Message: "Deployment requests 0 replicas — this would take the service offline.",
			})
		}
	}

	if !strings.Contains(newDeployment, "runAsNonRoot: true") {
		findings = append(findings, Finding{
			Severity: SeverityHigh, Rule: "must-run-as-nonroot", Source: "static",
			Message: "Deployment does not set runAsNonRoot: true.",
		})
	}

	if strings.Contains(newDeployment, "privileged: true") {
		findings = append(findings, Finding{
			Severity: SeverityCritical, Rule: "no-privileged-containers", Source: "static",
			Message: "Container requests privileged mode — full host access.",
		})
	}

	// Blast-radius: a large jump in replica count between old and new.
	oldReplicas := extractInt(oldDeployment, `replicas:\s*(\d+)`)
	newReplicas := extractInt(newDeployment, `replicas:\s*(\d+)`)
	if oldReplicas > 0 && newReplicas > oldReplicas*3 {
		findings = append(findings, Finding{
			Severity: SeverityMedium, Rule: "large-replica-jump", Source: "static",
			Message: fmt.Sprintf("Replica count jumping from %d to %d (>3x) — confirm this is intentional capacity planning, not a typo.", oldReplicas, newReplicas),
		})
	}

	return findings
}

func extractInt(s, pattern string) int {
	m := regexp.MustCompile(pattern).FindStringSubmatch(s)
	if len(m) != 2 {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

const systemPrompt = `You are a Kubernetes deployment risk reviewer for a GitOps platform.
You will be given: the app name, the previous Deployment manifest (may be empty if this is a first deploy), the new Deployment manifest, and a list of findings a static policy checker already raised.

Your job is to add ONLY findings the static checker would not catch — things that need contextual judgment: unusual or risky-looking env var names that look like secrets but aren't sourced from a secretRef, resource requests that look mismatched for the apparent workload type, or a diff that looks like it changes far more than a routine deploy should.

Do not repeat or restate the static findings. If you see nothing beyond the static findings, return an empty findings array — do not invent issues to seem thorough.

Respond ONLY with JSON matching exactly this shape, and nothing else:
{"riskScore": <integer 0-100, your holistic assessment of overall risk INCLUDING the static findings context you were given>, "findings": [{"severity": "low"|"medium"|"high"|"critical", "rule": "<short-rule-id>", "message": "<one sentence>"}], "summary": "<one or two sentence overall assessment>"}`

type aiResponse struct {
	RiskScore int `json:"riskScore"`
	Findings  []struct {
		Severity string `json:"severity"`
		Rule     string `json:"rule"`
		Message  string `json:"message"`
	} `json:"findings"`
	Summary string `json:"summary"`
}

// Review performs the full two-layer review. geminiClient may be nil, in
// which case only static checks run (useful for CI environments without
// GEMINI_API_KEY configured, or for testing) — AIReviewed will be false.
func Run(ctx context.Context, geminiClient *gemini.Client, appName, newDeployment, oldDeployment string) Review {
	findings := staticChecks(newDeployment, oldDeployment)
	staticScore := scoreFindings(findings)

	review := Review{
		RiskScore: staticScore,
		Findings:  findings,
	}

	if geminiClient != nil {
		userPrompt := fmt.Sprintf("App: %s\n\nPrevious Deployment manifest:\n%s\n\nNew Deployment manifest:\n%s\n\nStatic findings already raised:\n%s",
			appName, orNone(oldDeployment), newDeployment, formatFindings(findings))

		var ai aiResponse
		if err := geminiClient.GenerateJSON(ctx, systemPrompt, userPrompt, &ai); err == nil {
			review.AIReviewed = true
			for _, f := range ai.Findings {
				review.Findings = append(review.Findings, Finding{
					Severity: Severity(f.Severity), Rule: f.Rule, Message: f.Message, Source: "ai",
				})
			}
			review.Summary = ai.Summary
			// Take the higher of the two scores — AI can raise the bar
			// based on context, but a low AI score never overrides a
			// static critical/high finding's contribution.
			if ai.RiskScore > review.RiskScore {
				review.RiskScore = ai.RiskScore
			}
		}
		// If the AI call fails, we deliberately proceed with static-only
		// results rather than erroring the whole review — fail closed on
		// *scoring* (never silently approve), but don't block the
		// pipeline on an AI provider outage either.
	}

	if review.Summary == "" {
		review.Summary = fmt.Sprintf("%d static finding(s), risk score %d/100.", len(findings), review.RiskScore)
	}
	review.Approved = review.RiskScore < ApprovalThreshold
	return review
}

func scoreFindings(findings []Finding) int {
	score := 0
	for _, f := range findings {
		switch f.Severity {
		case SeverityCritical:
			score += 40
		case SeverityHigh:
			score += 25
		case SeverityMedium:
			score += 12
		case SeverityLow:
			score += 5
		}
	}
	if score > 100 {
		score = 100
	}
	return score
}

func formatFindings(findings []Finding) string {
	if len(findings) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for _, f := range findings {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", f.Severity, f.Rule, f.Message)
	}
	return b.String()
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none — this is a first deploy)"
	}
	return s
}
