# Builds every Helmsman CLI into one image.
#
# The CronJob manifest invokes /app/health-watcher; the same image also
# contains /app/pipeline, /app/risk-reviewer and the rest, so CI jobs and
# in-cluster jobs pull one artifact instead of seven.
#
# Build context is the module directory (gitops-ai-platform/):
#
#   docker build -f deploy/Dockerfile \
#     --build-arg VERSION=v1.0.0 \
#     --build-arg COMMIT="$(git rev-parse HEAD)" \
#     --build-arg DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
#     -t ghcr.io/zishaan1911/helmsman:v1.0.0 .

ARG GO_VERSION=1.23
ARG ALPINE_VERSION=3.20

# --platform=$BUILDPLATFORM keeps the toolchain native and cross-compiles
# instead, which is what makes a multi-arch build finish this decade.
FROM --platform=${BUILDPLATFORM} golang:${GO_VERSION}-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

ENV VERSION_PKG=github.com/zishaan1911/Helmsman/gitops-ai-platform/internal/version

WORKDIR /src
COPY . .

# No third-party dependencies, so there is no module download step to
# cache — the build cache mount is the only one worth having.
RUN --mount=type=cache,target=/root/.cache/go-build \
    set -eux; \
    for cmd in pipeline risk-reviewer health-watcher detector containerizer manifest-generator gitops-writer; do \
      CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
        go build -trimpath \
          -ldflags "-s -w -X ${VERSION_PKG}.Version=${VERSION} -X ${VERSION_PKG}.Commit=${COMMIT} -X ${VERSION_PKG}.Date=${DATE}" \
          -o "/out/${cmd}" "./cmd/${cmd}"; \
    done

FROM alpine:${ALPINE_VERSION}

ARG TARGETARCH
# Pinned deliberately. The previous version of this file resolved
# https://dl.k8s.io/release/stable.txt at build time, which meant the same
# Dockerfile produced a different image every week and an upstream
# regression could land in a released image with no commit to point at.
ARG KUBECTL_VERSION=v1.31.4
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

# git: pkg/gitopswriter shells out to it to commit and revert.
# kubectl: pkg/healthwatcher shells out to it to read rollout state.
# ca-certificates: pkg/gemini calls the API over HTTPS.
RUN set -eux; \
    apk add --no-cache git ca-certificates; \
    apk add --no-cache --virtual .fetch curl; \
    curl -fsSLo /usr/local/bin/kubectl \
      "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl"; \
    curl -fsSLo /tmp/kubectl.sha256 \
      "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl.sha256"; \
    echo "$(cat /tmp/kubectl.sha256)  /usr/local/bin/kubectl" | sha256sum -c -; \
    chmod +x /usr/local/bin/kubectl; \
    rm -f /tmp/kubectl.sha256; \
    apk del .fetch

WORKDIR /app
COPY --from=build /out/ /app/

# git refuses to operate in a directory owned by another user, which is
# exactly what a mounted GitOps repo looks like to an arbitrary UID.
RUN git config --system --add safe.directory '*'

USER 10001:10001

LABEL org.opencontainers.image.title="Helmsman" \
      org.opencontainers.image.description="GitOps deployment with an AI risk gate and self-healing rollback" \
      org.opencontainers.image.source="https://github.com/zishaan1911/Helmsman" \
      org.opencontainers.image.documentation="https://zishaan1911.github.io/Helmsman/" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${DATE}"

ENTRYPOINT ["/app/pipeline"]
