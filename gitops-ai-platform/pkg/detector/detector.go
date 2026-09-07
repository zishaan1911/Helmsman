// Package detector inspects a repository's working tree and infers the
// language, framework, build tool, run command, and likely listening port
// of the service it contains — with no configuration required from the
// developer. Detection is entirely deterministic (marker-file based); no
// AI call is involved here, since this is a solved problem that doesn't
// need probabilistic judgment.
package detector

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ServiceInfo is the detector's output: everything downstream stages
// (containerizer, manifest-generator) need to do their job.
type ServiceInfo struct {
	Language    string `json:"language"`                 // e.g. "node", "python", "go", "java", "ruby"
	Framework   string `json:"framework,omitempty"`      // e.g. "express", "flask", "django", "spring-boot"
	BuildTool   string `json:"buildTool,omitempty"`      // e.g. "npm", "pip", "go", "maven", "gradle", "bundler"
	Entrypoint  string `json:"entrypoint"`               // command to run the service, e.g. "node server.js"
	Port        int    `json:"port"`                     // best-guess listening port
	PackageMgr  string `json:"packageManager,omitempty"` // npm/yarn/pnpm distinction for node
	ModuleOrPkg string `json:"moduleOrPkg,omitempty"`    // go module path, python package name, etc.
	Confidence  string `json:"confidence"`               // "high", "medium", "low" — low means AI/human should double check
}

// marker describes a file whose presence identifies a language.
type marker struct {
	file     string
	language string
}

var languageMarkers = []marker{
	{"go.mod", "go"},
	{"package.json", "node"},
	{"requirements.txt", "python"},
	{"pyproject.toml", "python"},
	{"Pipfile", "python"},
	{"pom.xml", "java"},
	{"build.gradle", "java"},
	{"build.gradle.kts", "java"},
	{"Gemfile", "ruby"},
	{"Cargo.toml", "rust"},
}

// Detect walks repoPath (non-recursively at top level, matching standard
// monorepo-root conventions) and returns the best-guess ServiceInfo.
func Detect(repoPath string) (ServiceInfo, error) {
	info := ServiceInfo{Confidence: "medium"}

	present := map[string]bool{}
	for _, m := range languageMarkers {
		if fileExists(filepath.Join(repoPath, m.file)) {
			present[m.language] = true
		}
	}

	switch {
	case present["go"]:
		return detectGo(repoPath)
	case present["node"]:
		return detectNode(repoPath)
	case present["python"]:
		return detectPython(repoPath)
	case present["java"]:
		return detectJava(repoPath)
	case present["ruby"]:
		return detectRuby(repoPath)
	case present["rust"]:
		return detectRust(repoPath)
	}

	// Nothing recognized — fail closed rather than guess wildly.
	info.Language = "unknown"
	info.Confidence = "low"
	return info, fmt.Errorf("could not detect a supported language in %s (looked for: go.mod, package.json, requirements.txt/pyproject.toml, pom.xml/build.gradle, Gemfile, Cargo.toml)", repoPath)
}

func detectGo(repoPath string) (ServiceInfo, error) {
	info := ServiceInfo{Language: "go", BuildTool: "go", Port: 8080, Confidence: "high"}

	data, err := os.ReadFile(filepath.Join(repoPath, "go.mod"))
	if err == nil {
		re := regexp.MustCompile(`(?m)^module\s+(\S+)`)
		if m := re.FindStringSubmatch(string(data)); len(m) == 2 {
			info.ModuleOrPkg = m[1]
		}
	}

	// Common entrypoint locations, checked in priority order.
	for _, candidate := range []string{"main.go", "cmd/server/main.go", "cmd/api/main.go"} {
		if fileExists(filepath.Join(repoPath, candidate)) {
			info.Entrypoint = fmt.Sprintf("go run ./%s", filepath.Dir(candidate))
			if filepath.Dir(candidate) == "." {
				info.Entrypoint = "go run ."
			}
			break
		}
	}
	if info.Entrypoint == "" {
		info.Entrypoint = "go run ."
		info.Confidence = "medium"
	}

	if port := grepPort(repoPath, []string{"*.go"}); port != 0 {
		info.Port = port
	}
	return info, nil
}

type packageJSON struct {
	Main         string            `json:"main"`
	Scripts      map[string]string `json:"scripts"`
	Dependencies map[string]string `json:"dependencies"`
	Engines      map[string]string `json:"engines"`
}

func detectNode(repoPath string) (ServiceInfo, error) {
	info := ServiceInfo{Language: "node", BuildTool: "npm", PackageMgr: "npm", Port: 3000, Confidence: "high"}

	if fileExists(filepath.Join(repoPath, "yarn.lock")) {
		info.PackageMgr = "yarn"
	} else if fileExists(filepath.Join(repoPath, "pnpm-lock.yaml")) {
		info.PackageMgr = "pnpm"
	}

	data, err := os.ReadFile(filepath.Join(repoPath, "package.json"))
	if err != nil {
		return info, fmt.Errorf("reading package.json: %w", err)
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return info, fmt.Errorf("parsing package.json: %w", err)
	}

	// Framework inference from declared dependencies.
	frameworks := []struct {
		dep, name string
		port      int
	}{
		{"express", "express", 3000},
		{"fastify", "fastify", 3000},
		{"next", "next.js", 3000},
		{"@nestjs/core", "nestjs", 3000},
		{"koa", "koa", 3000},
	}
	for _, f := range frameworks {
		if _, ok := pkg.Dependencies[f.dep]; ok {
			info.Framework = f.name
			info.Port = f.port
			break
		}
	}

	runCmd := info.PackageMgr + " start"
	if _, ok := pkg.Scripts["start"]; ok {
		info.Entrypoint = runCmd
	} else if pkg.Main != "" {
		info.Entrypoint = fmt.Sprintf("node %s", pkg.Main)
	} else {
		info.Entrypoint = runCmd
		info.Confidence = "low" // no "start" script and no "main" — guessing
	}

	if port := grepPort(repoPath, []string{"*.js", "*.ts"}); port != 0 {
		info.Port = port
	}
	return info, nil
}

func detectPython(repoPath string) (ServiceInfo, error) {
	info := ServiceInfo{Language: "python", BuildTool: "pip", Port: 8000, Confidence: "medium"}

	reqFile := "requirements.txt"
	if fileExists(filepath.Join(repoPath, "pyproject.toml")) {
		info.BuildTool = "poetry/pip"
		reqFile = "pyproject.toml"
	}
	data, _ := os.ReadFile(filepath.Join(repoPath, reqFile))
	contents := strings.ToLower(string(data))

	switch {
	case strings.Contains(contents, "django"):
		info.Framework = "django"
		info.Port = 8000
		info.Entrypoint = "python manage.py runserver 0.0.0.0:8000"
		info.Confidence = "high"
	case strings.Contains(contents, "flask"):
		info.Framework = "flask"
		info.Port = 5000
		info.Entrypoint = detectPythonEntry(repoPath, "flask run --host=0.0.0.0")
		info.Confidence = "high"
	case strings.Contains(contents, "fastapi"):
		info.Framework = "fastapi"
		info.Port = 8000
		app := findFastAPIApp(repoPath)
		info.Entrypoint = fmt.Sprintf("uvicorn %s --host 0.0.0.0 --port 8000", app)
		info.Confidence = "high"
	default:
		info.Entrypoint = detectPythonEntry(repoPath, "python app.py")
		info.Confidence = "low"
	}

	if port := grepPort(repoPath, []string{"*.py"}); port != 0 {
		info.Port = port
	}
	return info, nil
}

func detectPythonEntry(repoPath, fallback string) string {
	for _, candidate := range []string{"app.py", "main.py", "wsgi.py", "server.py"} {
		if fileExists(filepath.Join(repoPath, candidate)) {
			return "python " + candidate
		}
	}
	return fallback
}

func findFastAPIApp(repoPath string) string {
	for _, candidate := range []string{"main:app", "app:app", "server:app"} {
		file := strings.Split(candidate, ":")[0] + ".py"
		if fileExists(filepath.Join(repoPath, file)) {
			return candidate
		}
	}
	return "main:app"
}

// springBootMarkers are the spellings a Spring Boot project actually uses.
// Maven POMs name the starter parent or the plugin artifact ("spring-boot"),
// while Gradle builds apply the plugin by its reverse-DNS id
// ("org.springframework.boot") and never contain the hyphenated form at all.
// Matching only the Maven spelling silently downgraded every Gradle Spring
// Boot service to a generic Java build.
var springBootMarkers = []string{"spring-boot", "org.springframework.boot"}

func detectJava(repoPath string) (ServiceInfo, error) {
	info := ServiceInfo{Language: "java", Port: 8080, Confidence: "medium"}

	var buildFiles []string
	if fileExists(filepath.Join(repoPath, "pom.xml")) {
		info.BuildTool = "maven"
		info.Entrypoint = "java -jar target/*.jar"
		buildFiles = []string{"pom.xml"}
	} else {
		info.BuildTool = "gradle"
		info.Entrypoint = "java -jar build/libs/*.jar"
		// Both DSLs are in languageMarkers, so both have to be read here.
		buildFiles = []string{"build.gradle", "build.gradle.kts"}
	}

	for _, name := range buildFiles {
		data, err := os.ReadFile(filepath.Join(repoPath, name))
		if err != nil {
			continue
		}
		contents := string(data)
		for _, marker := range springBootMarkers {
			if strings.Contains(contents, marker) {
				info.Framework = "spring-boot"
				info.Confidence = "high"
				return info, nil
			}
		}
	}

	return info, nil
}

func detectRuby(repoPath string) (ServiceInfo, error) {
	info := ServiceInfo{Language: "ruby", BuildTool: "bundler", Port: 3000, Confidence: "medium"}
	data, _ := os.ReadFile(filepath.Join(repoPath, "Gemfile"))
	if strings.Contains(string(data), "rails") {
		info.Framework = "rails"
		info.Entrypoint = "bundle exec rails server -b 0.0.0.0"
		info.Confidence = "high"
	} else if strings.Contains(string(data), "sinatra") {
		info.Framework = "sinatra"
		info.Entrypoint = "bundle exec ruby app.rb -o 0.0.0.0"
		info.Confidence = "high"
	} else {
		info.Entrypoint = "bundle exec ruby app.rb"
		info.Confidence = "low"
	}
	return info, nil
}

func detectRust(repoPath string) (ServiceInfo, error) {
	info := ServiceInfo{Language: "rust", BuildTool: "cargo", Port: 8080, Confidence: "medium"}
	data, err := os.ReadFile(filepath.Join(repoPath, "Cargo.toml"))
	if err == nil {
		re := regexp.MustCompile(`(?m)^name\s*=\s*"([^"]+)"`)
		if m := re.FindStringSubmatch(string(data)); len(m) == 2 {
			info.ModuleOrPkg = m[1]
			info.Entrypoint = fmt.Sprintf("./target/release/%s", m[1])
			info.Confidence = "high"
			return info, nil
		}
	}
	info.Entrypoint = "cargo run --release"
	return info, nil
}

// grepPort does a best-effort scan of source files for a hardcoded port
// (e.g. "listen(4000)", "PORT = 4000", ":4000"). This deliberately stays
// simple regex matching — it only overrides the framework default when it
// finds a confident, unambiguous match.
func grepPort(repoPath string, patterns []string) int {
	portRe := regexp.MustCompile(`(?i)(?:port|listen)\D{0,10}?(\d{4,5})\b`)
	var found int

	_ = filepath.WalkDir(repoPath, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && (d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		matched := false
		for _, p := range patterns {
			if ok, _ := filepath.Match(p, d.Name()); ok {
				matched = true
				break
			}
		}
		if !matched || found != 0 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if m := portRe.FindStringSubmatch(string(data)); len(m) == 2 {
			if p, err := strconv.Atoi(m[1]); err == nil && p > 1024 && p < 65536 {
				found = p
			}
		}
		return nil
	})
	return found
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
