package detector

import (
	"testing"
)

func TestDetectNode_PackageManagerFromLockfile(t *testing.T) {
	const pkgJSON = `{"scripts": {"start": "node server.js"}, "dependencies": {"express": "^4"}}`

	cases := []struct {
		name, lockfile, want string
	}{
		{"npm by default", "", "npm"},
		{"yarn", "yarn.lock", "yarn"},
		{"pnpm", "pnpm-lock.yaml", "pnpm"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "package.json", pkgJSON)
			if tc.lockfile != "" {
				writeFile(t, dir, tc.lockfile, "")
			}

			info, err := Detect(dir)
			if err != nil {
				t.Fatalf("Detect() error = %v", err)
			}
			if info.PackageMgr != tc.want {
				t.Errorf("PackageMgr = %q, want %q", info.PackageMgr, tc.want)
			}
			if info.Entrypoint != tc.want+" start" {
				t.Errorf("Entrypoint = %q, want %q", info.Entrypoint, tc.want+" start")
			}
		})
	}
}

// No start script and no main field means the run command is a guess. It
// has to be marked low-confidence, because that is the only signal
// containerizer has to flag the result for review.
func TestDetectNode_NoStartScriptIsLowConfidence(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"dependencies": {"koa": "^2"}}`)

	info, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if info.Confidence != "low" {
		t.Errorf("Confidence = %q, want low", info.Confidence)
	}
	if info.Framework != "koa" {
		t.Errorf("Framework = %q, want koa", info.Framework)
	}
}

func TestDetectNode_MainFieldUsedWhenThereIsNoStartScript(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"main": "index.js", "dependencies": {"fastify": "^4"}}`)

	info, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if info.Entrypoint != "node index.js" {
		t.Errorf("Entrypoint = %q, want %q", info.Entrypoint, "node index.js")
	}
}

func TestDetectNode_MalformedPackageJSONIsAnError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", "{ this is not json")

	if _, err := Detect(dir); err == nil {
		t.Fatal("Detect() = nil error, want an error for unparseable package.json")
	}
}

func TestDetectPython_Frameworks(t *testing.T) {
	cases := []struct {
		name, requirements, extraFile string
		wantFramework                 string
		wantPort                      int
		wantEntrypoint                string
	}{
		{"django", "django==5.0\n", "manage.py", "django", 8000, "python manage.py runserver 0.0.0.0:8000"},
		{"flask", "Flask==3.0.0\n", "app.py", "flask", 5000, "python app.py"},
		{"fastapi", "fastapi==0.111\nuvicorn\n", "main.py", "fastapi", 8000, "uvicorn main:app --host 0.0.0.0 --port 8000"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "requirements.txt", tc.requirements)
			writeFile(t, dir, tc.extraFile, "# app\n")

			info, err := Detect(dir)
			if err != nil {
				t.Fatalf("Detect() error = %v", err)
			}
			if info.Framework != tc.wantFramework {
				t.Errorf("Framework = %q, want %q", info.Framework, tc.wantFramework)
			}
			if info.Port != tc.wantPort {
				t.Errorf("Port = %d, want %d", info.Port, tc.wantPort)
			}
			if info.Entrypoint != tc.wantEntrypoint {
				t.Errorf("Entrypoint = %q, want %q", info.Entrypoint, tc.wantEntrypoint)
			}
			if info.Confidence != "high" {
				t.Errorf("Confidence = %q, want high for a recognised framework", info.Confidence)
			}
		})
	}
}

func TestDetectPython_PyprojectSelectsPoetryBuildTool(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", "[project]\nname = \"svc\"\ndependencies = [\"fastapi\"]\n")
	writeFile(t, dir, "main.py", "app = 1\n")

	info, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if info.BuildTool != "poetry/pip" {
		t.Errorf("BuildTool = %q, want poetry/pip", info.BuildTool)
	}
	if info.Framework != "fastapi" {
		t.Errorf("Framework = %q, want fastapi — pyproject.toml must be read for dependencies too", info.Framework)
	}
}

func TestDetectPython_UnrecognisedFrameworkIsLowConfidence(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "requests==2.31.0\n")

	info, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if info.Confidence != "low" {
		t.Errorf("Confidence = %q, want low for an unrecognised Python app", info.Confidence)
	}
}

func TestDetectJava(t *testing.T) {
	t.Run("maven spring boot", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "pom.xml", `<project><parent><artifactId>spring-boot-starter-parent</artifactId></parent></project>`)

		info, err := Detect(dir)
		if err != nil {
			t.Fatalf("Detect() error = %v", err)
		}
		if info.BuildTool != "maven" || info.Framework != "spring-boot" {
			t.Errorf("got BuildTool=%q Framework=%q, want maven/spring-boot", info.BuildTool, info.Framework)
		}
		if info.Confidence != "high" {
			t.Errorf("Confidence = %q, want high", info.Confidence)
		}
	})

	t.Run("gradle", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "build.gradle", "dependencies { implementation 'spring-boot-starter-web' }\n")

		info, err := Detect(dir)
		if err != nil {
			t.Fatalf("Detect() error = %v", err)
		}
		if info.BuildTool != "gradle" {
			t.Errorf("BuildTool = %q, want gradle", info.BuildTool)
		}
		if info.Framework != "spring-boot" {
			t.Errorf("Framework = %q, want spring-boot", info.Framework)
		}
		if info.Entrypoint != "java -jar build/libs/*.jar" {
			t.Errorf("Entrypoint = %q, want the gradle artifact path", info.Entrypoint)
		}
	})

	t.Run("plain maven, no framework", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "pom.xml", `<project><artifactId>plain</artifactId></project>`)

		info, err := Detect(dir)
		if err != nil {
			t.Fatalf("Detect() error = %v", err)
		}
		if info.Framework != "" {
			t.Errorf("Framework = %q, want empty", info.Framework)
		}
		if info.Confidence != "medium" {
			t.Errorf("Confidence = %q, want medium", info.Confidence)
		}
	})
}

func TestDetectRuby(t *testing.T) {
	cases := []struct {
		name, gemfile, wantFramework, wantConfidence string
	}{
		{"rails", "gem 'rails', '~> 7.1'\n", "rails", "high"},
		{"sinatra", "gem 'sinatra'\n", "sinatra", "high"},
		{"plain", "gem 'rake'\n", "", "low"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "Gemfile", tc.gemfile)

			info, err := Detect(dir)
			if err != nil {
				t.Fatalf("Detect() error = %v", err)
			}
			if info.Framework != tc.wantFramework {
				t.Errorf("Framework = %q, want %q", info.Framework, tc.wantFramework)
			}
			if info.Confidence != tc.wantConfidence {
				t.Errorf("Confidence = %q, want %q", info.Confidence, tc.wantConfidence)
			}
			if info.Entrypoint == "" {
				t.Error("Entrypoint is empty")
			}
		})
	}
}

func TestDetectRust_BinaryNameDrivesTheEntrypoint(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Cargo.toml", "[package]\nname = \"my-service\"\nversion = \"0.1.0\"\n")

	info, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if info.ModuleOrPkg != "my-service" {
		t.Errorf("ModuleOrPkg = %q, want my-service", info.ModuleOrPkg)
	}
	// containerizer's Rust template copies target/release/{{.ModuleOrPkg}},
	// so a wrong name here produces an image that cannot start.
	if info.Entrypoint != "./target/release/my-service" {
		t.Errorf("Entrypoint = %q, want ./target/release/my-service", info.Entrypoint)
	}
}

func TestDetectRust_NoPackageNameFallsBackToCargoRun(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Cargo.toml", "[workspace]\nmembers = [\"a\"]\n")

	info, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if info.Entrypoint != "cargo run --release" {
		t.Errorf("Entrypoint = %q, want the cargo run fallback", info.Entrypoint)
	}
}

func TestDetectGo_NestedEntrypoint(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/svc\n\ngo 1.23\n")
	mkdirAll(t, dir, "cmd/server")
	writeFile(t, dir, "cmd/server/main.go", "package main\nfunc main() {}\n")

	info, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if info.Entrypoint != "go run ./cmd/server" {
		t.Errorf("Entrypoint = %q, want ./cmd/server", info.Entrypoint)
	}
}

// Go wins over every other marker. A Go service that happens to ship a
// package.json for frontend tooling must not be containerized as Node.
func TestDetect_GoTakesPrecedenceOverNode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/svc\n")
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")
	writeFile(t, dir, "package.json", `{"dependencies": {"vite": "^5"}}`)

	info, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if info.Language != "go" {
		t.Errorf("Language = %q, want go", info.Language)
	}
}

func TestGrepPort_OverridesTheFrameworkDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"scripts":{"start":"node server.js"},"dependencies":{"express":"^4"}}`)
	writeFile(t, dir, "server.js", "const port = 4321;\napp.listen(port);\n")

	info, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if info.Port != 4321 {
		t.Errorf("Port = %d, want 4321 from the source scan", info.Port)
	}
}

func TestGrepPort_IgnoresOutOfRangeAndPrivilegedPorts(t *testing.T) {
	cases := []struct {
		name, source string
	}{
		{"privileged", "app.listen(80);\n"},
		{"above range", "const PORT = 999999;\n"},
		{"too few digits", "port: 8\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "package.json", `{"scripts":{"start":"node server.js"},"dependencies":{"express":"^4"}}`)
			writeFile(t, dir, "server.js", tc.source)

			info, err := Detect(dir)
			if err != nil {
				t.Fatalf("Detect() error = %v", err)
			}
			if info.Port != 3000 {
				t.Errorf("Port = %d, want the express default 3000 — %s ports must not override it", info.Port, tc.name)
			}
		})
	}
}

func TestGrepPort_SkipsVendoredDirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"scripts":{"start":"node server.js"},"dependencies":{"express":"^4"}}`)
	mkdirAll(t, dir, "node_modules/some-dep")
	writeFile(t, dir, "node_modules/some-dep/index.js", "server.listen(9999);\n")

	info, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if info.Port == 9999 {
		t.Error("a port from node_modules leaked into detection")
	}
}
