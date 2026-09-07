package platformconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "platform.yaml"), []byte(contents), 0o644); err != nil {
		t.Fatalf("writing platform.yaml: %v", err)
	}
	return dir
}

// A missing platform.yaml is the common case — most app repos never add
// one — so it must be a silent fall-through to defaults, not an error.
func TestLoad_NoFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(t.TempDir(), 8080)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil for a repo with no platform.yaml", err)
	}

	want := Default(8080)
	if cfg.Port != want.Port {
		t.Errorf("Port = %d, want %d (the detected port)", cfg.Port, want.Port)
	}
	if cfg.Replicas != want.Replicas {
		t.Errorf("Replicas = %d, want %d", cfg.Replicas, want.Replicas)
	}
	if cfg.Public {
		t.Error("Public = true, want false — apps must be private unless they opt in")
	}
	if cfg.Resources != want.Resources {
		t.Errorf("Resources = %+v, want %+v", cfg.Resources, want.Resources)
	}
}

// Every field is optional. A file that sets one field must not reset the
// others to zero values — that would silently strip an app's resource
// limits, which pkg/riskreview treats as a high-severity finding.
func TestLoad_PartialFileLayersOntoDefaults(t *testing.T) {
	dir := writeConfig(t, "replicas: 5\n")

	cfg, err := Load(dir, 9000)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Replicas != 5 {
		t.Errorf("Replicas = %d, want 5", cfg.Replicas)
	}
	if cfg.Port != 9000 {
		t.Errorf("Port = %d, want 9000 — an unset port must keep the detected value", cfg.Port)
	}
	if cfg.Resources != Default(9000).Resources {
		t.Errorf("Resources = %+v, want the defaults left intact", cfg.Resources)
	}
}

func TestLoad_FullFile(t *testing.T) {
	dir := writeConfig(t, `# deployment settings for this app
port: 4000
replicas: 3
public: true
resources:
  cpuRequest: "150m"
  memoryRequest: "192Mi"
  cpuLimit: "750m"
  memoryLimit: "768Mi"
env:
  - name: LOG_LEVEL, value: info
  - name: DATABASE_URL, secretRef: sample-app-secrets/database-url
`)

	cfg, err := Load(dir, 3000)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Port != 4000 {
		t.Errorf("Port = %d, want 4000 — platform.yaml must override the detected port", cfg.Port)
	}
	if cfg.Replicas != 3 {
		t.Errorf("Replicas = %d, want 3", cfg.Replicas)
	}
	if !cfg.Public {
		t.Error("Public = false, want true")
	}

	wantRes := Resources{CPURequest: "150m", MemoryRequest: "192Mi", CPULimit: "750m", MemoryLimit: "768Mi"}
	if cfg.Resources != wantRes {
		t.Errorf("Resources = %+v, want %+v", cfg.Resources, wantRes)
	}

	if len(cfg.Env) != 2 {
		t.Fatalf("len(Env) = %d, want 2: %+v", len(cfg.Env), cfg.Env)
	}
	if cfg.Env[0] != (EnvVar{Name: "LOG_LEVEL", Value: "info"}) {
		t.Errorf("Env[0] = %+v, want a literal LOG_LEVEL=info", cfg.Env[0])
	}
	if cfg.Env[1] != (EnvVar{Name: "DATABASE_URL", SecretRef: "sample-app-secrets/database-url"}) {
		t.Errorf("Env[1] = %+v, want a secretRef-sourced DATABASE_URL", cfg.Env[1])
	}
	if cfg.Env[1].Value != "" {
		t.Error("a secretRef entry must not also carry a literal Value — that would inline the secret into the manifest")
	}
}

// The parser is indentation-sensitive by hand. Once a top-level key is
// seen, the `resources:` and `env:` sections must be closed, or a later
// top-level `port:` could be swallowed as if it were nested.
func TestLoad_SectionsCloseAtTopLevelKeys(t *testing.T) {
	dir := writeConfig(t, `resources:
  cpuLimit: "1"
env:
  - name: A, value: 1
public: true
replicas: 4
`)

	cfg, err := Load(dir, 8080)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Resources.CPULimit != "1" {
		t.Errorf("CPULimit = %q, want %q", cfg.Resources.CPULimit, "1")
	}
	if len(cfg.Env) != 1 {
		t.Errorf("len(Env) = %d, want 1 — `public:` must end the env list, not join it", len(cfg.Env))
	}
	if !cfg.Public {
		t.Error("Public = false — a top-level key after a section must still be read")
	}
	if cfg.Replicas != 4 {
		t.Errorf("Replicas = %d, want 4", cfg.Replicas)
	}
}

func TestLoad_CommentsAndBlankLinesIgnored(t *testing.T) {
	dir := writeConfig(t, `
# leading comment

port: 7000

# another comment
replicas: 1
`)

	cfg, err := Load(dir, 8080)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Port != 7000 || cfg.Replicas != 1 {
		t.Errorf("cfg = %+v, want Port=7000 Replicas=1", cfg)
	}
}

// A typo'd number must not silently become zero: zero replicas takes the
// service offline, and pkg/riskreview only catches that after the fact.
func TestLoad_NonNumericValuesKeepTheFallback(t *testing.T) {
	dir := writeConfig(t, "replicas: three\nport: eighty\n")

	cfg, err := Load(dir, 8080)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Replicas != Default(8080).Replicas {
		t.Errorf("Replicas = %d, want the default %d — an unparseable value must not become 0", cfg.Replicas, Default(8080).Replicas)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want the detected 8080", cfg.Port)
	}
}

func TestLoad_QuotedAndUnquotedScalarsAreEquivalent(t *testing.T) {
	quoted := writeConfig(t, "resources:\n  cpuRequest: \"250m\"\n")
	bare := writeConfig(t, "resources:\n  cpuRequest: 250m\n")

	a, err := Load(quoted, 8080)
	if err != nil {
		t.Fatalf("Load(quoted) error = %v", err)
	}
	b, err := Load(bare, 8080)
	if err != nil {
		t.Fatalf("Load(bare) error = %v", err)
	}
	if a.Resources.CPURequest != "250m" || b.Resources.CPURequest != "250m" {
		t.Errorf("quoted = %q, bare = %q, want both %q", a.Resources.CPURequest, b.Resources.CPURequest, "250m")
	}
}

func TestLoad_EnvEntryWithoutNameIsAnError(t *testing.T) {
	dir := writeConfig(t, "env:\n  - value: orphaned\n")

	if _, err := Load(dir, 8080); err == nil {
		t.Fatal("Load() = nil error, want an error for an env entry with no name")
	}
}

func TestLoad_PublicIsOptInOnly(t *testing.T) {
	for _, val := range []string{"false", "yes", "1", "TRUE", ""} {
		dir := writeConfig(t, "public: "+val+"\n")
		cfg, err := Load(dir, 8080)
		if err != nil {
			t.Fatalf("Load(public: %q) error = %v", val, err)
		}
		if cfg.Public {
			t.Errorf("public: %q produced Public=true; only the exact literal `true` may expose a service", val)
		}
	}
}

func TestDefault_IsConservative(t *testing.T) {
	cfg := Default(8080)
	if cfg.Replicas < 2 {
		t.Errorf("Replicas = %d, want at least 2 so a single pod restart isn't an outage", cfg.Replicas)
	}
	if cfg.Resources.CPULimit == "" || cfg.Resources.MemoryLimit == "" {
		t.Error("defaults must set resource limits — pkg/riskreview flags their absence as high severity")
	}
	if cfg.Public {
		t.Error("Public must default to false")
	}
}
