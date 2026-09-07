// Command pipeline runs the full "push code, get a deployed app" flow
// (weeks 1-6 of the platform roadmap):
//
//	detect -> containerize -> generate manifests -> commit to GitOps repo
//
// It deliberately stops short of an actual `docker build`/push and an
// actual ArgoCD sync — those require a container registry and a live
// cluster respectively, neither of which exist in this environment — but
// every artifact it produces (Dockerfile, K8s manifests, GitOps commit,
// ArgoCD Application) is real and would work unmodified against real
// infrastructure.
//
// Usage:
//
//	pipeline -app-repo ./examples/sample-app -gitops-repo ./gitops -app myapp \
//	  -image registry.example.com/myapp:dev -env staging
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/internal/version"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/containerizer"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/detector"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/gitopswriter"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/manifest"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/platformconfig"
)

func main() {
	appRepo := flag.String("app-repo", "", "path to the application source repo (required)")
	gitopsRepo := flag.String("gitops-repo", "", "path to a locally cloned GitOps repo (required)")
	appName := flag.String("app", "", "application name (required)")
	image := flag.String("image", "", "container image reference (required)")
	env := flag.String("env", "staging", "deployment environment")
	gitopsRepoURL := flag.String("gitops-repo-url", "", "GitOps repo URL, used only to render the ArgoCD Application (optional)")
	push := flag.Bool("push", false, "push the GitOps commit after writing it")
	showVersion := version.Flag()
	flag.Parse()
	version.Exit(*showVersion, "pipeline")

	if *appRepo == "" || *gitopsRepo == "" || *appName == "" || *image == "" {
		fmt.Fprintln(os.Stderr, "pipeline: -app-repo, -gitops-repo, -app, and -image are required")
		os.Exit(1)
	}

	step("1/4 detect")
	info, err := detector.Detect(*appRepo)
	must(err, "detection failed")
	fmt.Printf("  language=%s framework=%s entrypoint=%q port=%d confidence=%s\n",
		info.Language, info.Framework, info.Entrypoint, info.Port, info.Confidence)

	step("2/4 containerize")
	cres, err := containerizer.Generate(info, *appRepo)
	must(err, "containerization failed")
	fmt.Printf("  wrote %s/Dockerfile\n", *appRepo)
	if cres.NeedsReview {
		fmt.Printf("  ⚠ needs review: %s\n", cres.ReviewReason)
	}

	step("3/4 generate manifests")
	cfg, err := platformconfig.Load(*appRepo, info.Port)
	must(err, "loading platform.yaml failed")
	out, err := manifest.Generate(manifest.Input{
		AppName:   *appName,
		Namespace: *appName,
		Image:     *image,
		Env:       *env,
		Service:   info,
		Config:    cfg,
	})
	must(err, "manifest generation failed")
	fmt.Printf("  generated deployment.yaml, service.yaml, kustomization.yaml%s\n", ingressNote(out.Ingress))
	fmt.Printf("  replicas=%d cpu=%s/%s mem=%s/%s public=%v\n",
		cfg.Replicas, cfg.Resources.CPURequest, cfg.Resources.CPULimit,
		cfg.Resources.MemoryRequest, cfg.Resources.MemoryLimit, cfg.Public)

	step("4/4 write to GitOps repo")
	result, err := gitopswriter.Write(gitopswriter.WriteRequest{
		GitopsRepoPath: *gitopsRepo,
		AppName:        *appName,
		Env:            *env,
		Manifests:      out,
		Push:           *push,
	})
	must(err, "gitops write failed")
	fmt.Printf("  wrote %d files under %s\n", len(result.FilesWritten), result.AppDir)
	if result.Committed {
		fmt.Printf("  committed %s\n", result.CommitSHA)
	} else {
		fmt.Println("  no changes (manifests identical to last run)")
	}
	if result.Pushed {
		fmt.Println("  pushed")
	}

	if *gitopsRepoURL != "" {
		fmt.Println()
		fmt.Println("ArgoCD Application (apply once, to onboard this app):")
		fmt.Println(gitopswriter.ArgoApplication(*appName, *env, *gitopsRepoURL, "main", *appName))
	}

	fmt.Println()
	fmt.Println("Next: ArgoCD/Flux (already watching this GitOps repo) will pick up the commit and reconcile the cluster.")
}

func step(name string) {
	fmt.Println()
	fmt.Println("== " + name + " ==")
}

func ingressNote(ingress string) string {
	if ingress == "" {
		return ""
	}
	return ", ingress.yaml"
}

func must(err error, msg string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "pipeline: %s: %v\n", msg, err)
		os.Exit(1)
	}
}
