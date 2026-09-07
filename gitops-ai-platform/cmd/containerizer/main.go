// Command containerizer runs detection then writes a Dockerfile into the repo.
// Usage: containerizer -repo ./path/to/app
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/containerizer"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/detector"
)

func main() {
	repo := flag.String("repo", ".", "path to the application repository")
	flag.Parse()

	info, err := detector.Detect(*repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "containerizer: detection failed:", err)
		os.Exit(1)
	}

	res, err := containerizer.Generate(info, *repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "containerizer:", err)
		os.Exit(1)
	}

	fmt.Printf("wrote %s/Dockerfile (language=%s framework=%s port=%d)\n", *repo, info.Language, info.Framework, info.Port)
	if res.NeedsReview {
		fmt.Printf("⚠ needs review: %s\n", res.ReviewReason)
	}
}
