// Command gitops-writer commits already-generated manifests into a GitOps
// repo. In the full pipeline this is called as a library (see cmd/pipeline);
// this CLI exists for scripting/debugging that step in isolation by piping
// in files from a directory.
//
// Usage: gitops-writer -gitops-repo ./gitops -app myapp -env staging -manifests-dir ./out
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/internal/version"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/gitopswriter"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/manifest"
)

func main() {
	gitopsRepo := flag.String("gitops-repo", "", "path to a locally cloned GitOps repo (required)")
	appName := flag.String("app", "", "application name (required)")
	env := flag.String("env", "staging", "deployment environment")
	manifestsDir := flag.String("manifests-dir", "", "directory containing deployment.yaml/service.yaml/ingress.yaml/kustomization.yaml (required)")
	push := flag.Bool("push", false, "push after committing")
	dryRun := flag.Bool("dry-run", false, "write files without committing")
	showVersion := version.Flag()
	flag.Parse()
	version.Exit(*showVersion, "gitops-writer")

	if *gitopsRepo == "" || *appName == "" || *manifestsDir == "" {
		fmt.Fprintln(os.Stderr, "gitops-writer: -gitops-repo, -app, and -manifests-dir are required")
		os.Exit(1)
	}

	out := manifest.Output{
		Deployment:    readIfExists(filepath.Join(*manifestsDir, "deployment.yaml")),
		Service:       readIfExists(filepath.Join(*manifestsDir, "service.yaml")),
		Ingress:       readIfExists(filepath.Join(*manifestsDir, "ingress.yaml")),
		Kustomization: readIfExists(filepath.Join(*manifestsDir, "kustomization.yaml")),
	}

	result, err := gitopswriter.Write(gitopswriter.WriteRequest{
		GitopsRepoPath: *gitopsRepo,
		AppName:        *appName,
		Env:            *env,
		Manifests:      out,
		Push:           *push,
		DryRun:         *dryRun,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitops-writer:", err)
		os.Exit(1)
	}

	fmt.Printf("wrote %d files to %s\n", len(result.FilesWritten), result.AppDir)
	if result.Committed {
		fmt.Printf("committed %s\n", result.CommitSHA)
	} else {
		fmt.Println("no changes to commit")
	}
	if result.Pushed {
		fmt.Println("pushed")
	}
}

func readIfExists(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}
