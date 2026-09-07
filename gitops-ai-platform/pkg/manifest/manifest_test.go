package manifest

import (
	"strings"
	"testing"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/detector"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/platformconfig"
)

func TestGenerateProducesExpectedPortAndReplicas(t *testing.T) {
	in := Input{
		AppName:   "sample-app",
		Namespace: "sample-app",
		Image:     "registry.example.com/sample-app:sha1",
		Env:       "staging",
		Service:   detector.ServiceInfo{Language: "node", Port: 4000},
		Config: platformconfig.Config{
			Port:     4000,
			Replicas: 3,
			Public:   true,
			Resources: platformconfig.Resources{
				CPURequest: "150m", MemoryRequest: "192Mi",
				CPULimit: "750m", MemoryLimit: "768Mi",
			},
		},
	}

	out, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if !strings.Contains(out.Deployment, "containerPort: 4000") {
		t.Error("Deployment missing expected containerPort")
	}
	if !strings.Contains(out.Deployment, "replicas: 3") {
		t.Error("Deployment missing expected replicas")
	}
	if out.Ingress == "" {
		t.Error("expected Ingress to be generated when Config.Public is true")
	}
	if !strings.Contains(out.Kustomization, "ingress.yaml") {
		t.Error("Kustomization should reference ingress.yaml when public")
	}
}

func TestGenerateOmitsIngressWhenNotPublic(t *testing.T) {
	in := Input{
		AppName: "internal-app",
		Image:   "registry.example.com/internal-app:sha1",
		Service: detector.ServiceInfo{Language: "go", Port: 8080},
		Config:  platformconfig.Default(8080),
	}

	out, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if out.Ingress != "" {
		t.Error("expected no Ingress when Config.Public is false")
	}
	if strings.Contains(out.Kustomization, "ingress.yaml") {
		t.Error("Kustomization should not reference ingress.yaml when not public")
	}
}

func TestGenerateRejectsZeroPort(t *testing.T) {
	in := Input{
		AppName: "broken-app",
		Image:   "registry.example.com/broken-app:sha1",
		Service: detector.ServiceInfo{Language: "node"},
		Config:  platformconfig.Config{}, // Port left at zero
	}

	if _, err := Generate(in); err == nil {
		t.Fatal("Generate() expected an error for zero port, got nil")
	}
}
