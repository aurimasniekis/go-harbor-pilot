# syntax=docker/dockerfile:1.7
#
# Harbor Pilot, plain image: gateway, control plane and UI in one static
# binary. API-key and local providers only; for the Claude Code and Codex
# bridges use deploy/Dockerfile.bridge.

# ---- Frontend build ----
# Runs on the build host's platform: the web assets are the same for every
# target, and an emulated pnpm install is slow and can hang (docs/RELEASING.md).
FROM --platform=$BUILDPLATFORM node:24.11.1-alpine3.22 AS web
WORKDIR /src/web
RUN corepack enable && corepack prepare pnpm@11.23.0 --activate
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml web/.npmrc ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN mkdir -p /src/internal/web/dist && pnpm run build

# ---- Backend build ----
# Also on the build host's platform; the binary is cross-compiled for the
# target (pure Go, CGO_ENABLED=0).
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.23 AS go
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
COPY api ./api
COPY --from=web /src/internal/web/dist ./internal/web/dist
ARG VERSION=docker
ARG TARGETOS TARGETARCH
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/harbor-pilot ./cmd/harbor-pilot \
 && mkdir -p /out/data

# ---- Runtime ----
# distroless static: no shell, no package manager, non-root (65532).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go /out/harbor-pilot /usr/local/bin/harbor-pilot
COPY --from=go --chown=65532:65532 /out/data /data
USER 65532:65532
ENV HP_DATA_DIR=/data \
    HP_LISTEN=:8080 \
    HP_MASTER_KEY_FILE=/run/secrets/master_key
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/usr/local/bin/harbor-pilot", "healthcheck", "--url", "http://127.0.0.1:8080/readyz"]
ENTRYPOINT ["/usr/local/bin/harbor-pilot"]
CMD ["serve"]
