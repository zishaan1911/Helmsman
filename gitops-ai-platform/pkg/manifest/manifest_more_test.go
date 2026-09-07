package manifest

import (
	"strings"
	"testing"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/detector"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/platformconfig"
)

func baseInput() Input {
	return Input{
		AppName:   "sample-app",
		Namespace: "sample-app",
		Image:     "registry.example.com/sample-app:sha-abc123",
		Env:       "staging",
		Service:   detector.ServiceInfo{Language: "node", Port: 4000},
		Config:    platformconfig.Default(4000),
	}
}

// pkg/riskreview's static checks are string matches against this exact
// output. If the templates drift, the risk gate stops finding what it is
// looking for and starts passing everything — a silent failure with no
// error anywhere. These assertions are the contract between the two.
func TestGenerate_DeploymentSatisfiesTheStaticRiskChecks(t *testing.T) {
	out, err := Generate(baseInput())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	for _, want := range []string{"limits:", "runAsNonRoot: true"} {
		if !strings.Contains(out.Deployment, want) {
			t.Errorf("Deployment is missing %q, which pkg/riskreview greps for:\n%s", want, out.Deployment)
		}
	}
	if strings.Contains(out.Deployment, "privileged: true") {
		t.Error("generated Deployment requests privileged mode")
	}
	if strings.Contains(out.Deployment, "replicas: 0") {
		t.Error("generated Deployment has zero replicas")
	}
	if !strings.Contains(out.Deployment, "allowPrivilegeEscalation: false") {
		t.Error("Deployment should set allowPrivilegeEscalation: false")
	}
}

func TestGenerate_LiteralEnvVars(t *testing.T) {
	in := baseInput()
	in.Config.Env = []platformconfig.EnvVar{{Name: "LOG_LEVEL", Value: "info"}}

	out, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if !strings.Contains(out.Deployment, "- name: LOG_LEVEL") {
		t.Errorf("Deployment missing the env var name:\n%s", out.Deployment)
	}
	if !strings.Contains(out.Deployment, `value: "info"`) {
		t.Errorf("Deployment missing the quoted literal value:\n%s", out.Deployment)
	}
}

// A secretRef must render as a secretKeyRef and never as a literal value.
// The generated manifest is committed to a Git repo and fed to Gemini for
// review, so an inlined secret would leak into both.
func TestGenerate_SecretRefNeverInlinesTheValue(t *testing.T) {
	in := baseInput()
	in.Config.Env = []platformconfig.EnvVar{
		{Name: "DATABASE_URL", SecretRef: "sample-app-secrets/database-url"},
	}

	out, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	for _, want := range []string{
		"- name: DATABASE_URL",
		"valueFrom:",
		"secretKeyRef:",
		"name: sample-app-secrets",
		"key: database-url",
	} {
		if !strings.Contains(out.Deployment, want) {
			t.Errorf("Deployment missing %q:\n%s", want, out.Deployment)
		}
	}
	if strings.Contains(out.Deployment, "value: \"\"") {
		t.Error("a secretRef env var also rendered an empty literal value")
	}
}

func TestGenerate_SecretRefWithoutAKeyFallsBackToValue(t *testing.T) {
	in := baseInput()
	in.Config.Env = []platformconfig.EnvVar{{Name: "TOKEN", SecretRef: "my-secret"}}

	out, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if !strings.Contains(out.Deployment, "name: my-secret") {
		t.Errorf("Deployment missing the secret name:\n%s", out.Deployment)
	}
	if !strings.Contains(out.Deployment, "key: value") {
		t.Errorf("a secretRef with no /key should default to key `value`:\n%s", out.Deployment)
	}
}

func TestGenerate_MixedEnvVars(t *testing.T) {
	in := baseInput()
	in.Config.Env = []platformconfig.EnvVar{
		{Name: "LOG_LEVEL", Value: "debug"},
		{Name: "API_KEY", SecretRef: "app-secrets/api-key"},
		{Name: "REGION", Value: "eu-west-1"},
	}

	out, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if got := strings.Count(out.Deployment, "- name: "); got < 3 {
		t.Errorf("expected 3 env entries, found %d:\n%s", got, out.Deployment)
	}
	if !strings.Contains(out.Deployment, "secretKeyRef:") {
		t.Error("the secretRef entry did not render")
	}
	if !strings.Contains(out.Deployment, `value: "eu-west-1"`) {
		t.Error("a literal entry after a secretRef entry did not render")
	}
}

func TestGenerate_NoEnvSectionWhenThereAreNoEnvVars(t *testing.T) {
	out, err := Generate(baseInput())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if strings.Contains(out.Deployment, "env:") {
		t.Errorf("an empty env list should omit the env: key entirely:\n%s", out.Deployment)
	}
}

func TestGenerate_NamespaceDefaultsAndPropagates(t *testing.T) {
	in := baseInput()
	in.Namespace = ""

	out, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	for name, doc := range map[string]string{
		"Deployment":    out.Deployment,
		"Service":       out.Service,
		"Kustomization": out.Kustomization,
	} {
		if !strings.Contains(doc, "namespace: default") {
			t.Errorf("%s did not fall back to the default namespace:\n%s", name, doc)
		}
	}
}

func TestGenerate_ServiceTargetsTheContainerPort(t *testing.T) {
	in := baseInput()
	in.Config.Port = 4000

	out, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	// Port 80 in, container port out: an Ingress that routes to port 80
	// only works if the Service publishes 80 regardless of the app's port.
	if !strings.Contains(out.Service, "port: 80") {
		t.Errorf("Service should publish port 80:\n%s", out.Service)
	}
	if !strings.Contains(out.Service, "targetPort: 4000") {
		t.Errorf("Service should target the container port:\n%s", out.Service)
	}
	if !strings.Contains(out.Service, "type: ClusterIP") {
		t.Error("Service should be ClusterIP; exposure is the Ingress's job")
	}
}

func TestGenerate_IngressRoutesToTheService(t *testing.T) {
	in := baseInput()
	in.Config.Public = true

	out, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if !strings.Contains(out.Ingress, "host: sample-app.staging.example.com") {
		t.Errorf("Ingress host should be <app>.<env>.<domain>:\n%s", out.Ingress)
	}
	if !strings.Contains(out.Ingress, "name: sample-app") {
		t.Errorf("Ingress backend should name the Service:\n%s", out.Ingress)
	}
	if !strings.Contains(out.Ingress, "number: 80") {
		t.Errorf("Ingress backend port must match the Service's published port:\n%s", out.Ingress)
	}
}

// Every object carries managed-by so an operator can tell at a glance what
// wrote it, and so `kubectl get -l managed-by=...` finds the whole set.
func TestGenerate_EverythingIsLabelledManagedBy(t *testing.T) {
	in := baseInput()
	in.Config.Public = true

	out, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	for name, doc := range map[string]string{
		"Deployment": out.Deployment,
		"Service":    out.Service,
		"Ingress":    out.Ingress,
	} {
		if !strings.Contains(doc, "managed-by: gitops-ai-platform") {
			t.Errorf("%s is missing the managed-by label:\n%s", name, doc)
		}
	}
}

func TestGenerate_SelectorMatchesPodLabels(t *testing.T) {
	out, err := Generate(baseInput())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	// A selector that doesn't match the pod template is the classic
	// "Deployment reports zero ready replicas forever" bug.
	if strings.Count(out.Deployment, "app: sample-app") < 3 {
		t.Errorf("expected the app label on metadata, selector and pod template:\n%s", out.Deployment)
	}
	if !strings.Contains(out.Service, "selector:\n    app: sample-app") {
		t.Errorf("Service selector must match the Deployment's pod labels:\n%s", out.Service)
	}
}

func TestGenerate_ProbesUseTheResolvedPort(t *testing.T) {
	in := baseInput()
	in.Config.Port = 9876

	out, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if !strings.Contains(out.Deployment, "readinessProbe:") || !strings.Contains(out.Deployment, "livenessProbe:") {
		t.Error("Deployment should define both probes; healthwatcher relies on rollout status")
	}
	// containerPort + both probes.
	if got := strings.Count(out.Deployment, "9876"); got < 3 {
		t.Errorf("expected the port on the container and both probes, found %d occurrences:\n%s", got, out.Deployment)
	}
}

func TestGenerate_IsDeterministic(t *testing.T) {
	in := baseInput()
	in.Config.Public = true
	in.Config.Env = []platformconfig.EnvVar{
		{Name: "A", Value: "1"},
		{Name: "B", SecretRef: "s/k"},
	}

	first, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := Generate(in)
		if err != nil {
			t.Fatalf("Generate() error = %v", err)
		}
		if again != first {
			t.Fatal("Generate() is not deterministic; identical input produced different YAML, which would churn the GitOps repo on every run")
		}
	}
}

func TestGenerate_KustomizationListsExactlyWhatWasWritten(t *testing.T) {
	private, err := Generate(baseInput())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if strings.Contains(private.Kustomization, "ingress.yaml") {
		t.Error("kustomization references ingress.yaml but no Ingress was generated — kustomize build would fail")
	}

	in := baseInput()
	in.Config.Public = true
	public, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	for _, want := range []string{"deployment.yaml", "service.yaml", "ingress.yaml"} {
		if !strings.Contains(public.Kustomization, want) {
			t.Errorf("kustomization missing %q:\n%s", want, public.Kustomization)
		}
	}
}
