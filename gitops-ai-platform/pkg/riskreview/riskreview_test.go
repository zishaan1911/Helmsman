package riskreview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/gemini"
)

const safeDeployment = `apiVersion: apps/v1
kind: Deployment
spec:
  replicas: 2
  template:
    spec:
      containers:
        - resources:
            limits:
              cpu: 500m
          securityContext:
            runAsNonRoot: true
`

const riskyDeployment = `apiVersion: apps/v1
kind: Deployment
spec:
  replicas: 0
  template:
    spec:
      containers:
        - securityContext:
            privileged: true
`

func TestRun_StaticOnly_SafeDeployment(t *testing.T) {
	review := Run(context.Background(), nil, "myapp", safeDeployment, "")
	if !review.Approved {
		t.Errorf("expected safe deployment to be approved, got riskScore=%d findings=%v", review.RiskScore, review.Findings)
	}
	if review.AIReviewed {
		t.Error("expected AIReviewed=false when geminiClient is nil")
	}
}

func TestRun_StaticOnly_RiskyDeployment(t *testing.T) {
	review := Run(context.Background(), nil, "myapp", riskyDeployment, "")
	if review.Approved {
		t.Errorf("expected risky deployment (0 replicas, privileged) to be rejected, got riskScore=%d", review.RiskScore)
	}

	var hasZeroReplicas, hasPrivileged bool
	for _, f := range review.Findings {
		if f.Rule == "zero-replicas" {
			hasZeroReplicas = true
		}
		if f.Rule == "no-privileged-containers" {
			hasPrivileged = true
		}
	}
	if !hasZeroReplicas {
		t.Error("expected a zero-replicas finding")
	}
	if !hasPrivileged {
		t.Error("expected a no-privileged-containers finding")
	}
}

func TestRun_LargeReplicaJumpFlagged(t *testing.T) {
	old := "spec:\n  replicas: 2\n"
	next := "spec:\n  replicas: 20\n  template:\n    spec:\n      containers:\n        - resources:\n            limits:\n              cpu: 1\n          securityContext:\n            runAsNonRoot: true\n"
	review := Run(context.Background(), nil, "myapp", next, old)

	var found bool
	for _, f := range review.Findings {
		if f.Rule == "large-replica-jump" {
			found = true
		}
	}
	if !found {
		t.Error("expected a large-replica-jump finding when replicas go from 2 to 20")
	}
}

func TestRun_AIFindingsAreAdditive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := struct {
			Candidates []map[string]interface{} `json:"candidates"`
		}{
			Candidates: []map[string]interface{}{
				{"content": map[string]interface{}{"parts": []map[string]string{
					{"text": `{"riskScore": 70, "findings": [{"severity":"high","rule":"suspicious-env-var","message":"DATABASE_PASSWORD is set as a literal value, not a secretRef"}], "summary": "Looks like a plaintext secret in env vars."}`},
				}}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := &gemini.Client{APIKey: "test", BaseURL: srv.URL, HTTPClient: srv.Client()}
	review := Run(context.Background(), client, "myapp", safeDeployment, "")

	if !review.AIReviewed {
		t.Fatal("expected AIReviewed=true")
	}
	if review.RiskScore != 70 {
		t.Errorf("RiskScore = %d, want 70 (AI score should win since it's higher than static)", review.RiskScore)
	}
	if review.Approved {
		t.Error("expected review to be rejected given AI riskScore=70 >= threshold")
	}

	var found bool
	for _, f := range review.Findings {
		if f.Rule == "suspicious-env-var" && f.Source == "ai" {
			found = true
		}
	}
	if !found {
		t.Error("expected the AI finding to be present alongside static findings")
	}
}

func TestRun_AIFailureFailsClosedToStaticResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := &gemini.Client{APIKey: "test", BaseURL: srv.URL, HTTPClient: srv.Client()}
	review := Run(context.Background(), client, "myapp", riskyDeployment, "")

	if review.AIReviewed {
		t.Error("expected AIReviewed=false when the AI call fails")
	}
	if review.Approved {
		t.Error("expected the risky deployment to still be rejected on static findings alone")
	}
	if !strings.Contains(review.Summary, "static finding") {
		t.Errorf("expected fallback summary to mention static findings, got %q", review.Summary)
	}
}
