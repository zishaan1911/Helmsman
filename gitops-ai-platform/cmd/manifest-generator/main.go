// Command manifest-generator produces K8s YAML for a detected service.
// Usage: manifest-generator -repo ./app -app myapp -image registry/myapp:sha -env staging
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/internal/version"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/detector"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/manifest"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/platformconfig"
)

func main() {
	repo := flag.String("repo", ".", "path to the application repository")
	appName := flag.String("app", "", "application name (required)")
	image := flag.String("image", "", "container image, e.g. registry/app:sha (required)")
	env := flag.String("env", "staging", "deployment environment")
	namespace := flag.String("namespace", "", "target namespace (defaults to app name)")
	showVersion := version.Flag()
	flag.Parse()
	version.Exit(*showVersion, "manifest-generator")

	if *appName == "" || *image == "" {
		fmt.Fprintln(os.Stderr, "manifest-generator: -app and -image are required")
		os.Exit(1)
	}
	if *namespace == "" {
		*namespace = *appName
	}

	info, err := detector.Detect(*repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "manifest-generator: detection failed:", err)
		os.Exit(1)
	}

	cfg, err := platformconfig.Load(*repo, info.Port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "manifest-generator: loading platform.yaml:", err)
		os.Exit(1)
	}

	out, err := manifest.Generate(manifest.Input{
		AppName:   *appName,
		Namespace: *namespace,
		Image:     *image,
		Env:       *env,
		Service:   info,
		Config:    cfg,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "manifest-generator:", err)
		os.Exit(1)
	}

	fmt.Println("--- deployment.yaml ---")
	fmt.Println(out.Deployment)
	fmt.Println("--- service.yaml ---")
	fmt.Println(out.Service)
	if out.Ingress != "" {
		fmt.Println("--- ingress.yaml ---")
		fmt.Println(out.Ingress)
	}
	fmt.Println("--- kustomization.yaml ---")
	fmt.Println(out.Kustomization)
}
