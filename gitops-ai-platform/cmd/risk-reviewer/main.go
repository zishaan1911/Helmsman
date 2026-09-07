// Command risk-reviewer scores a proposed Deployment manifest change and
// exits non-zero if the risk score is at or above riskreview.ApprovalThreshold.
// Designed to run as a GitHub Actions PR check against the diff the
// pipeline would produce, gating auto-merge into the GitOps repo.
//
// Usage:
//
//	risk-reviewer -app myapp -new ./new-deployment.yaml -old ./old-deployment.yaml
//
// GEMINI_API_KEY must be set in the environment for the AI-augmented layer
// to run; if unset, only static policy checks run (still meaningful, just
// less contextual).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/gemini"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/riskreview"
)

func main() {
	appName := flag.String("app", "", "application name (required)")
	newPath := flag.String("new", "", "path to the new deployment.yaml (required)")
	oldPath := flag.String("old", "", "path to the previous deployment.yaml (optional — omit for a first deploy)")
	jsonOut := flag.Bool("json", false, "print machine-readable JSON instead of a human summary")
	flag.Parse()

	if *appName == "" || *newPath == "" {
		fmt.Fprintln(os.Stderr, "risk-reviewer: -app and -new are required")
		os.Exit(2)
	}

	newManifest, err := os.ReadFile(*newPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "risk-reviewer: reading -new:", err)
		os.Exit(2)
	}
	var oldManifest []byte
	if *oldPath != "" {
		oldManifest, err = os.ReadFile(*oldPath)
		if err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "risk-reviewer: reading -old:", err)
			os.Exit(2)
		}
	}

	var client *gemini.Client
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		client = gemini.New(key)
	}

	review := riskreview.Run(context.Background(), client, *appName, string(newManifest), string(oldManifest))

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(review)
	} else {
		printHuman(*appName, review, client != nil)
	}

	if !review.Approved {
		os.Exit(1) // non-zero exit fails the CI check / blocks auto-merge
	}
}

func printHuman(appName string, review riskreview.Review, hadClient bool) {
	fmt.Printf("Risk review for %s: score=%d/100 approved=%v\n", appName, review.RiskScore, review.Approved)
	fmt.Println(review.Summary)
	if hadClient && !review.AIReviewed {
		fmt.Println("⚠ AI review was requested but failed — falling back to static findings only.")
	}
	if len(review.Findings) == 0 {
		fmt.Println("No findings.")
		return
	}
	fmt.Println()
	for _, f := range review.Findings {
		fmt.Printf("  [%s/%s] (%s) %s\n", f.Severity, f.Source, f.Rule, f.Message)
	}
}
