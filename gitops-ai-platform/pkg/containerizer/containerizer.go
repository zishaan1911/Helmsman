// Package containerizer turns a detector.ServiceInfo into a working,
// reasonably production-minded Dockerfile: multi-stage where it matters,
// non-root user, minimal base image. Templates cover the common-case
// frameworks deterministically. Anything the templates don't recognize
// (low-confidence detection, unusual entrypoint) is flagged via NeedsReview
// so the caller can route it to an AI-assisted pass or a human instead of
// silently shipping a guess.
package containerizer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/zishaan1911/Helmsman/gitops-ai-platform/pkg/detector"
)

// Result is the containerizer's output.
type Result struct {
	Dockerfile          string
	DockerignoreContent string
	NeedsReview         bool
	ReviewReason        string
}

var nodeTemplate = template.Must(template.New("node").Parse(`# syntax=docker/dockerfile:1
FROM node:20-slim AS deps
WORKDIR /app
COPY package*.json ./
RUN npm ci --omit=dev

FROM node:20-slim
WORKDIR /app
RUN useradd --uid 10001 --shell /usr/sbin/nologin appuser
COPY --from=deps /app/node_modules ./node_modules
COPY . .
USER appuser
EXPOSE {{.Port}}
CMD [{{range $i, $part := .EntrypointParts}}{{if $i}}, {{end}}"{{$part}}"{{end}}]
`))

var pythonTemplate = template.Must(template.New("python").Parse(`# syntax=docker/dockerfile:1
FROM python:3.12-slim
WORKDIR /app
RUN useradd --uid 10001 --shell /usr/sbin/nologin appuser
COPY requirements.txt* pyproject.toml* ./
RUN if [ -f requirements.txt ]; then pip install --no-cache-dir -r requirements.txt; \
    elif [ -f pyproject.toml ]; then pip install --no-cache-dir .; fi
COPY . .
USER appuser
EXPOSE {{.Port}}
CMD [{{range $i, $part := .EntrypointParts}}{{if $i}}, {{end}}"{{$part}}"{{end}}]
`))

var goTemplate = template.Must(template.New("go").Parse(`# syntax=docker/dockerfile:1
FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/app .

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/app /app
USER 10001
EXPOSE {{.Port}}
ENTRYPOINT ["/app"]
`))

var javaTemplate = template.Must(template.New("java").Parse(`# syntax=docker/dockerfile:1
FROM eclipse-temurin:21-jdk AS build
WORKDIR /src
COPY . .
RUN {{if eq .BuildTool "maven"}}./mvnw -q -DskipTests package{{else}}./gradlew -q build -x test{{end}}

FROM eclipse-temurin:21-jre
WORKDIR /app
RUN useradd --uid 10001 --shell /usr/sbin/nologin appuser
COPY --from=build /src/{{if eq .BuildTool "maven"}}target{{else}}build/libs{{end}}/*.jar app.jar
USER appuser
EXPOSE {{.Port}}
ENTRYPOINT ["java", "-jar", "app.jar"]
`))

var rubyTemplate = template.Must(template.New("ruby").Parse(`# syntax=docker/dockerfile:1
FROM ruby:3.3-slim
WORKDIR /app
RUN apt-get update -qq && apt-get install -y --no-install-recommends build-essential && rm -rf /var/lib/apt/lists/*
RUN useradd --uid 10001 --shell /usr/sbin/nologin appuser
COPY Gemfile Gemfile.lock* ./
RUN bundle install
COPY . .
USER appuser
EXPOSE {{.Port}}
CMD [{{range $i, $part := .EntrypointParts}}{{if $i}}, {{end}}"{{$part}}"{{end}}]
`))

var rustTemplate = template.Must(template.New("rust").Parse(`# syntax=docker/dockerfile:1
FROM rust:1.79 AS build
WORKDIR /src
COPY . .
RUN cargo build --release

FROM gcr.io/distroless/cc-debian12
COPY --from=build /src/target/release/{{.ModuleOrPkg}} /app
USER 10001
EXPOSE {{.Port}}
ENTRYPOINT ["/app"]
`))

var dockerignoreDefault = `.git
node_modules
__pycache__
*.pyc
target
build
.env
.env.local
*.log
`

// Generate produces a Dockerfile for the given ServiceInfo and writes it
// (plus a .dockerignore) into repoPath. It returns the content and whether
// this generation should be flagged for human/AI review before being
// trusted in an automated pipeline.
func Generate(info detector.ServiceInfo, repoPath string) (Result, error) {
	res := Result{DockerignoreContent: dockerignoreDefault}

	if info.Confidence == "low" {
		res.NeedsReview = true
		res.ReviewReason = "detector had low confidence in the entrypoint — verify the CMD before trusting this build"
	}

	var tmpl *template.Template
	switch info.Language {
	case "node":
		tmpl = nodeTemplate
	case "python":
		tmpl = pythonTemplate
	case "go":
		tmpl = goTemplate
	case "java":
		tmpl = javaTemplate
	case "ruby":
		tmpl = rubyTemplate
	case "rust":
		tmpl = rustTemplate
	default:
		return res, fmt.Errorf("no Dockerfile template for language %q — needs AI-assisted or manual containerization", info.Language)
	}

	data := struct {
		detector.ServiceInfo
		EntrypointParts []string
	}{
		ServiceInfo:     info,
		EntrypointParts: splitEntrypoint(info.Entrypoint),
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return res, fmt.Errorf("rendering Dockerfile template: %w", err)
	}
	res.Dockerfile = buf.String()

	if repoPath != "" {
		if err := os.WriteFile(filepath.Join(repoPath, "Dockerfile"), buf.Bytes(), 0o644); err != nil {
			return res, fmt.Errorf("writing Dockerfile: %w", err)
		}
		if err := os.WriteFile(filepath.Join(repoPath, ".dockerignore"), []byte(res.DockerignoreContent), 0o644); err != nil {
			return res, fmt.Errorf("writing .dockerignore: %w", err)
		}
	}

	return res, nil
}

// splitEntrypoint turns "npm start" into ["npm", "start"] for exec-form CMD.
// Deliberately simple (space split) — entrypoints with quoted args aren't
// produced by the detector today, so this stays intentionally minimal.
func splitEntrypoint(entrypoint string) []string {
	var parts []string
	var current []rune
	for _, r := range entrypoint {
		if r == ' ' {
			if len(current) > 0 {
				parts = append(parts, string(current))
				current = nil
			}
			continue
		}
		current = append(current, r)
	}
	if len(current) > 0 {
		parts = append(parts, string(current))
	}
	return parts
}
