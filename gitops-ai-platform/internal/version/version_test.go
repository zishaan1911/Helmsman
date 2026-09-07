package version

import (
	"strings"
	"testing"
)

func TestGet_FillsInRuntimeFacts(t *testing.T) {
	info := Get()
	if info.GoVersion == "" {
		t.Error("GoVersion is empty")
	}
	if !strings.Contains(info.Platform, "/") {
		t.Errorf("Platform = %q, want GOOS/GOARCH", info.Platform)
	}
	if info.Version == "" {
		t.Error("Version is empty; an unstamped build must still report something")
	}
}

func TestLine_IncludesEveryFieldAndAbbreviatesTheSHA(t *testing.T) {
	origV, origC, origD := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = origV, origC, origD })

	Version, Commit, Date = "v1.0.0", "9f3c1b2a4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f90", "2026-09-07T09:14:22Z"

	line := Line("pipeline")
	for _, want := range []string{"helmsman", "pipeline", "v1.0.0", "9f3c1b2a4d5e", "2026-09-07T09:14:22Z"} {
		if !strings.Contains(line, want) {
			t.Errorf("Line() = %q, missing %q", line, want)
		}
	}
	if strings.Contains(line, Commit) {
		t.Errorf("Line() should abbreviate the SHA to 12 characters, got %q", line)
	}
	if strings.Contains(line, "\n") {
		t.Error("Line() must be a single line")
	}
}

// An unstamped `go build` must not claim to be a release. Reporting a
// version the maintainers never tagged is worse than reporting nothing.
func TestLine_UnstampedBuildSaysDev(t *testing.T) {
	origV, origC, origD := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = origV, origC, origD })

	Version, Commit, Date = "dev", "unknown", "unknown"

	line := Line("detector")
	if !strings.Contains(line, "dev") {
		t.Errorf("Line() = %q, want it to report dev", line)
	}
	if strings.Contains(line, "v1.") {
		t.Errorf("Line() = %q, an unstamped build must not look like a release", line)
	}
}

func TestLine_ShortCommitIsNotTruncated(t *testing.T) {
	orig := Commit
	t.Cleanup(func() { Commit = orig })

	Commit = "abc123"
	if !strings.Contains(Line("detector"), "abc123") {
		t.Errorf("a commit shorter than 12 characters was mangled: %q", Line("detector"))
	}
}
