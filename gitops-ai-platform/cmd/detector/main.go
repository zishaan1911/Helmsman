// Command detector inspects a repo and prints its ServiceInfo as JSON.
// Usage: detector -repo ./path/to/app
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/internal/version"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/detector"
)

func main() {
	repo := flag.String("repo", ".", "path to the application repository")
	showVersion := version.Flag()
	flag.Parse()
	version.Exit(*showVersion, "detector")

	info, err := detector.Detect(*repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "detector:", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(info); err != nil {
		fmt.Fprintln(os.Stderr, "detector: encoding output:", err)
		os.Exit(1)
	}
}
