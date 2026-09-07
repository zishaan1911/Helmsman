// Package manifest generates Kubernetes YAML (Deployment, Service, and
// optionally Ingress) from a detector.ServiceInfo + platformconfig.Config.
//
// Deliberately template-based rather than free-form AI generation: the
// structure of a Deployment/Service is well-defined and shouldn't vary
// call to call. The "AI-generated YAML" failure mode (subtly invalid or
// inconsistent manifests) is designed out at this layer. Where the AI risk
// -reviewer (a separate package, see docs/architecture) adds value is in
// judging whether the *values* — resource limits, replica counts, exposed
// ports — are sane, not in generating the YAML shape itself.
package manifest

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/detector"
	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/platformconfig"
)

// Input bundles everything the templates need.
type Input struct {
	AppName   string
	Namespace string
	Image     string
	Env       string // deployment environment, e.g. "staging", "production"
	Service   detector.ServiceInfo
	Config    platformconfig.Config
}

// Output holds the rendered manifest files, ready to be written to a
// GitOps repo.
type Output struct {
	Deployment    string
	Service       string
	Ingress       string // empty if Config.Public is false
	Kustomization string
}

var funcMap = template.FuncMap{
	"secretName": func(ref string) string {
		parts := splitOnce(ref, '/')
		return parts[0]
	},
	"secretKey": func(ref string) string {
		parts := splitOnce(ref, '/')
		if len(parts) == 2 {
			return parts[1]
		}
		return "value"
	},
}

func splitOnce(s string, sep byte) []string {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}

var deploymentTemplate = template.Must(template.New("deployment").Funcs(funcMap).Parse(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{.AppName}}
  namespace: {{.Namespace}}
  labels:
    app: {{.AppName}}
    managed-by: gitops-ai-platform
spec:
  replicas: {{.Config.Replicas}}
  selector:
    matchLabels:
      app: {{.AppName}}
  template:
    metadata:
      labels:
        app: {{.AppName}}
    spec:
      containers:
        - name: {{.AppName}}
          image: {{.Image}}
          ports:
            - containerPort: {{.Config.Port}}
          resources:
            requests:
              cpu: {{.Config.Resources.CPURequest}}
              memory: {{.Config.Resources.MemoryRequest}}
            limits:
              cpu: {{.Config.Resources.CPULimit}}
              memory: {{.Config.Resources.MemoryLimit}}
          readinessProbe:
            tcpSocket:
              port: {{.Config.Port}}
            initialDelaySeconds: 5
            periodSeconds: 10
          livenessProbe:
            tcpSocket:
              port: {{.Config.Port}}
            initialDelaySeconds: 15
            periodSeconds: 20
          securityContext:
            runAsNonRoot: true
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: false
{{- if .Config.Env}}
          env:
{{- range .Config.Env}}
{{- if .SecretRef}}
            - name: {{.Name}}
              valueFrom:
                secretKeyRef:
                  name: {{secretName .SecretRef}}
                  key: {{secretKey .SecretRef}}
{{- else}}
            - name: {{.Name}}
              value: "{{.Value}}"
{{- end}}
{{- end}}
{{- end}}
`))

var serviceTemplate = template.Must(template.New("service").Parse(`apiVersion: v1
kind: Service
metadata:
  name: {{.AppName}}
  namespace: {{.Namespace}}
  labels:
    app: {{.AppName}}
    managed-by: gitops-ai-platform
spec:
  selector:
    app: {{.AppName}}
  ports:
    - port: 80
      targetPort: {{.Config.Port}}
  type: ClusterIP
`))

var ingressTemplate = template.Must(template.New("ingress").Parse(`apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{.AppName}}
  namespace: {{.Namespace}}
  labels:
    app: {{.AppName}}
    managed-by: gitops-ai-platform
spec:
  rules:
    - host: {{.AppName}}.{{.Env}}.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: {{.AppName}}
                port:
                  number: 80
`))

var kustomizationTemplate = template.Must(template.New("kustomization").Parse(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: {{.Namespace}}
resources:
  - deployment.yaml
  - service.yaml
{{- if .Config.Public}}
  - ingress.yaml
{{- end}}
`))

// Generate renders all manifest files for the given Input.
func Generate(in Input) (Output, error) {
	if in.Namespace == "" {
		in.Namespace = "default"
	}
	if in.Config.Port == 0 {
		return Output{}, fmt.Errorf("no port resolved for %s — refusing to generate a manifest with no containerPort", in.AppName)
	}

	var out Output
	var err error

	if out.Deployment, err = render(deploymentTemplate, in); err != nil {
		return out, fmt.Errorf("rendering Deployment: %w", err)
	}
	if out.Service, err = render(serviceTemplate, in); err != nil {
		return out, fmt.Errorf("rendering Service: %w", err)
	}
	if in.Config.Public {
		if out.Ingress, err = render(ingressTemplate, in); err != nil {
			return out, fmt.Errorf("rendering Ingress: %w", err)
		}
	}
	if out.Kustomization, err = render(kustomizationTemplate, in); err != nil {
		return out, fmt.Errorf("rendering Kustomization: %w", err)
	}

	return out, nil
}

func render(tmpl *template.Template, in Input) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, in); err != nil {
		return "", err
	}
	return buf.String(), nil
}
