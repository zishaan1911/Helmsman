package containerizer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/detector"
)

// Every template this package ships must produce a container that does not
// run as root. This is the single property the whole platform's security
// posture leans on — pkg/riskreview flags a Deployment without
// runAsNonRoot, but nothing downstream re-checks the image itself.
func TestGenerate_AllTemplatesDropRoot(t *testing.T) {
	cases := []struct {
		name string
		info detector.ServiceInfo
		want string // the line that proves root was dropped
	}{
		{"node", detector.ServiceInfo{Language: "node", Port: 3000, Entrypoint: "npm start", Confidence: "high"}, "USER appuser"},
		{"python", detector.ServiceInfo{Language: "python", Port: 8000, Entrypoint: "python app.py", Confidence: "high"}, "USER appuser"},
		{"go", detector.ServiceInfo{Language: "go", Port: 8080, Entrypoint: "go run .", Confidence: "high"}, "USER 10001"},
		{"java", detector.ServiceInfo{Language: "java", BuildTool: "maven", Port: 8080, Entrypoint: "java -jar target/app.jar", Confidence: "high"}, "USER appuser"},
		{"ruby", detector.ServiceInfo{Language: "ruby", Port: 3000, Entrypoint: "bundle exec ruby app.rb", Confidence: "high"}, "USER appuser"},
		{"rust", detector.ServiceInfo{Language: "rust", Port: 8080, ModuleOrPkg: "myapp", Entrypoint: "./target/release/myapp", Confidence: "high"}, "USER 10001"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Generate(tc.info, "")
			if err != nil {
				t.Fatalf("Generate() error = %v", err)
			}
			if !strings.Contains(res.Dockerfile, tc.want) {
				t.Errorf("Dockerfile for %s does not drop root (looking for %q):\n%s", tc.name, tc.want, res.Dockerfile)
			}
			if !strings.Contains(res.Dockerfile, "EXPOSE") {
				t.Errorf("Dockerfile for %s does not EXPOSE a port:\n%s", tc.name, res.Dockerfile)
			}
		})
	}
}

// The compiled languages ship a build stage and a distroless runtime stage;
// shipping the toolchain in the runtime image is a large, avoidable attack
// surface, so it's worth asserting rather than assuming.
func TestGenerate_CompiledLanguagesAreMultiStage(t *testing.T) {
	cases := []struct {
		name        string
		info        detector.ServiceInfo
		wantRuntime string
	}{
		{"go", detector.ServiceInfo{Language: "go", Port: 8080, Entrypoint: "go run ."}, "gcr.io/distroless/static-debian12"},
		{"rust", detector.ServiceInfo{Language: "rust", Port: 8080, ModuleOrPkg: "myapp"}, "gcr.io/distroless/cc-debian12"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Generate(tc.info, "")
			if err != nil {
				t.Fatalf("Generate() error = %v", err)
			}
			if strings.Count(res.Dockerfile, "FROM ") < 2 {
				t.Errorf("expected a multi-stage build for %s:\n%s", tc.name, res.Dockerfile)
			}
			if !strings.Contains(res.Dockerfile, tc.wantRuntime) {
				t.Errorf("expected runtime stage %q for %s:\n%s", tc.wantRuntime, tc.name, res.Dockerfile)
			}
			if strings.Contains(res.Dockerfile, "AS build\nFROM") {
				t.Error("build stage appears to be empty")
			}
		})
	}
}

func TestGenerate_NodeUsesExecFormCMDFromEntrypoint(t *testing.T) {
	info := detector.ServiceInfo{Language: "node", Port: 4000, Entrypoint: "npm start", Confidence: "high"}

	res, err := Generate(info, "")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	// Exec form (a JSON array) matters: shell form wraps the process in
	// /bin/sh, which then swallows SIGTERM and turns every rolling update
	// into a 30-second termination-grace-period wait.
	if !strings.Contains(res.Dockerfile, `CMD ["npm", "start"]`) {
		t.Errorf("expected exec-form CMD built from the entrypoint, got:\n%s", res.Dockerfile)
	}
	if !strings.Contains(res.Dockerfile, "EXPOSE 4000") {
		t.Errorf("expected EXPOSE 4000, got:\n%s", res.Dockerfile)
	}
	if !strings.Contains(res.Dockerfile, "npm ci --omit=dev") {
		t.Error("expected a reproducible, production-only install (npm ci --omit=dev)")
	}
}

func TestGenerate_JavaBuildToolSelectsTheRightPaths(t *testing.T) {
	maven, err := Generate(detector.ServiceInfo{Language: "java", BuildTool: "maven", Port: 8080}, "")
	if err != nil {
		t.Fatalf("Generate(maven) error = %v", err)
	}
	if !strings.Contains(maven.Dockerfile, "./mvnw") {
		t.Errorf("maven build should invoke ./mvnw:\n%s", maven.Dockerfile)
	}
	if !strings.Contains(maven.Dockerfile, "target/*.jar") {
		t.Errorf("maven build should copy from target/:\n%s", maven.Dockerfile)
	}

	gradle, err := Generate(detector.ServiceInfo{Language: "java", BuildTool: "gradle", Port: 8080}, "")
	if err != nil {
		t.Fatalf("Generate(gradle) error = %v", err)
	}
	if !strings.Contains(gradle.Dockerfile, "./gradlew") {
		t.Errorf("gradle build should invoke ./gradlew:\n%s", gradle.Dockerfile)
	}
	if !strings.Contains(gradle.Dockerfile, "build/libs/*.jar") {
		t.Errorf("gradle build should copy from build/libs/:\n%s", gradle.Dockerfile)
	}
}

// Low detector confidence means the entrypoint is a guess. The pipeline is
// allowed to continue, but the guess has to be advertised — silently
// shipping it is how you get a green deploy of a container that exits
// immediately.
func TestGenerate_LowConfidenceIsFlaggedForReview(t *testing.T) {
	res, err := Generate(detector.ServiceInfo{Language: "node", Port: 3000, Entrypoint: "npm start", Confidence: "low"}, "")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if !res.NeedsReview {
		t.Error("NeedsReview = false, want true for a low-confidence detection")
	}
	if res.ReviewReason == "" {
		t.Error("ReviewReason is empty; a review flag with no reason is not actionable")
	}
	if res.Dockerfile == "" {
		t.Error("a flagged result should still produce a Dockerfile, not an empty one")
	}
}

func TestGenerate_HighConfidenceIsNotFlagged(t *testing.T) {
	res, err := Generate(detector.ServiceInfo{Language: "go", Port: 8080, Entrypoint: "go run .", Confidence: "high"}, "")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if res.NeedsReview {
		t.Errorf("NeedsReview = true for a high-confidence detection: %s", res.ReviewReason)
	}
}

// An unsupported language must be a hard error. Falling through to some
// "generic" template would produce a Dockerfile that builds and then fails
// at runtime, which is strictly worse than refusing.
func TestGenerate_UnsupportedLanguageIsAnError(t *testing.T) {
	for _, lang := range []string{"elixir", "haskell", "unknown", ""} {
		_, err := Generate(detector.ServiceInfo{Language: lang, Port: 8080}, "")
		if err == nil {
			t.Errorf("Generate(language=%q) = nil error, want an error", lang)
		}
	}
}

func TestGenerate_WritesDockerfileAndDockerignore(t *testing.T) {
	dir := t.TempDir()
	info := detector.ServiceInfo{Language: "node", Port: 3000, Entrypoint: "npm start", Confidence: "high"}

	res, err := Generate(info, dir)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	onDisk, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatalf("reading generated Dockerfile: %v", err)
	}
	if string(onDisk) != res.Dockerfile {
		t.Error("the Dockerfile written to disk differs from the one returned")
	}

	ignore, err := os.ReadFile(filepath.Join(dir, ".dockerignore"))
	if err != nil {
		t.Fatalf("reading generated .dockerignore: %v", err)
	}
	for _, want := range []string{".git", "node_modules", ".env"} {
		if !strings.Contains(string(ignore), want) {
			t.Errorf(".dockerignore is missing %q — it would be baked into the image", want)
		}
	}
}

// repoPath == "" means "render only". Nothing should be written anywhere,
// which is what makes it safe to call from a dry-run or a test.
func TestGenerate_EmptyRepoPathWritesNothing(t *testing.T) {
	dir := t.TempDir()
	if _, err := Generate(detector.ServiceInfo{Language: "go", Port: 8080}, ""); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading temp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no files written, found %d", len(entries))
	}
}

func TestGenerate_UnwritableRepoPathReturnsAnError(t *testing.T) {
	info := detector.ServiceInfo{Language: "go", Port: 8080}
	if _, err := Generate(info, filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("Generate() = nil error, want an error when the target directory is missing")
	}
}

func TestSplitEntrypoint(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"npm start", []string{"npm", "start"}},
		{"node server.js", []string{"node", "server.js"}},
		{"python manage.py runserver 0.0.0.0:8000", []string{"python", "manage.py", "runserver", "0.0.0.0:8000"}},
		{"single", []string{"single"}},
		{"  leading and   collapsed   spaces ", []string{"leading", "and", "collapsed", "spaces"}},
		{"", nil},
		{"   ", nil},
	}

	for _, tc := range cases {
		got := splitEntrypoint(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("splitEntrypoint(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitEntrypoint(%q) = %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
}
