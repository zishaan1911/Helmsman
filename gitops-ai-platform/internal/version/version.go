// Package version carries the build identity of every Helmsman CLI.
//
// The values are injected at link time (-ldflags "-X ...") by GoReleaser
// and by deploy/Dockerfile. A `go build` or `go run` with no ldflags
// leaves the defaults in place, which is why they say "dev" and
// "unknown" rather than pretending to be a release.
//
// This matters more than it looks: the CLIs write commits into a repo
// that reconciles into a cluster. When a manifest turns out to be wrong,
// the first question is which build produced it, and "the binary someone
// had on their laptop in March" is not an answer.
package version

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
)

// Injected at link time. Do not set these anywhere else.
var (
	// Version is the release tag, e.g. "v1.0.0".
	Version = "dev"

	// Commit is the full Git SHA the binary was built from.
	Commit = "unknown"

	// Date is the build timestamp, RFC 3339, UTC.
	Date = "unknown"
)

// Info is the resolved build identity.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
}

// Get returns the build identity, falling back to the module metadata the
// Go toolchain embeds automatically. That fallback is what makes
// `go install ...@v1.0.0` report a real version instead of "dev", since
// nothing passes ldflags on that path.
func Get() Info {
	info := Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}

	if info.Version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		info.Version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if info.Commit == "unknown" {
				info.Commit = s.Value
			}
		case "vcs.time":
			if info.Date == "unknown" {
				info.Date = s.Value
			}
		case "vcs.modified":
			if s.Value == "true" {
				info.Commit += "-dirty"
			}
		}
	}
	return info
}

// Line is the one-line build identity for the named command, e.g.
//
//	helmsman pipeline v1.0.0 (commit 9f3c1b2a4d5e, built 2026-09-07T09:14:22Z, go1.23.0, linux/amd64)
func Line(command string) string {
	i := Get()
	commit := i.Commit
	if commit != "unknown" && len(commit) > 12 {
		commit = commit[:12]
	}
	return fmt.Sprintf("helmsman %s %s (commit %s, built %s, %s, %s)",
		command, i.Version, commit, i.Date, i.GoVersion, i.Platform)
}

// Flag registers -version on the default FlagSet. Call it before
// flag.Parse(), and pass the result to Exit afterwards.
func Flag() *bool {
	return flag.Bool("version", false, "print version information and exit")
}

// Exit prints the build line and exits 0 if -version was passed. Call it
// immediately after flag.Parse(), before any required-flag validation —
// asking a binary what version it is should never fail on missing flags.
func Exit(requested bool, command string) {
	if requested {
		fmt.Println(Line(command))
		os.Exit(0)
	}
}
