# =============================================================================
# Code-Agent Docker Build
# =============================================================================
#
# Build targets:
#   docker build -t code-agent:latest .                              # default = server (distroless)
#   docker build --target server-dev -t code-agent:dev .             # debian-slim for debugging
#   docker build --target cli -t code-agent-cli:latest .            # CLI tool
#   docker build --target host-agent -t code-agent-host:latest .    # Host agent
#
# Run:
#   docker compose up -d                          # middleware only
#   docker compose --profile app up -d --build    # full stack incl. server
#   docker compose --profile dev up -d --build    # full stack with dev image
#   docker run -it code-agent-cli:latest          # CLI access to server
#
# Notes:
#   - distroless images have NO shell: no RUN, no HEALTHCHECK, no debugging
#   - Use server-dev target for debugging (has bash, wget, ca-certificates)
#   - Healthcheck for distroless is configured in docker-compose.yml
#   - All runtime data dirs (/data/workspace, /data/eino-checkpoints, /data/objects)
#     are created via mounted volumes in docker-compose, NOT baked into the image
# =============================================================================

# ---- build stage -----------------------------------------------------------
FROM golang:1.22-bookworm AS builder

WORKDIR /src

# Leverage Docker layer cache: module download cached unless go.mod/go.sum change
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build all binaries
COPY . .

ARG VERSION=dev
ARG GIT_COMMIT=unknown

RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${GIT_COMMIT}" \
      -o /out/server ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux \
    go build -trimpath \
      -ldflags="-s -w" \
      -o /out/cli ./cmd/cli \
 && CGO_ENABLED=0 GOOS=linux \
    go build -trimpath \
      -ldflags="-s -w" \
      -o /out/host-agent ./cmd/host-agent


# ---- runtime: server (production, distroless, minimal attack surface) --------
FROM gcr.io/distroless/static-debian12:nonroot AS server

LABEL org.opencontainers.image.title="code-agent" \
      org.opencontainers.image.description="Code-Agent: AI coding agent server" \
      org.opencontainers.image.source="https://github.com/spray272598/code-agent" \
      org.opencontainers.image.vendor="Code-Agent"

WORKDIR /app

# Binary + runtime assets
COPY --from=builder /out/server /app/server
COPY configs/config.example.yaml /app/configs/config.yaml
COPY scripts/sql/01_schema.sql /app/scripts/sql/01_schema.sql
COPY skills /app/skills
COPY teams /app/teams
COPY commands /app/commands
COPY hooks /app/hooks

# distroless has no shell; runtime dirs are created via volume mounts (docker-compose)
USER nonroot:nonroot
EXPOSE 8080

# Sensible defaults (overridden by docker-compose or -e flags)
ENV LLM_USE_MOCK=true \
    DB_TYPE=memory \
    REDIS_ENABLED=false \
    OTLP_ENABLED=false \
    LOG_LEVEL=info

ENTRYPOINT ["/app/server"]
CMD ["-config", "/app/configs/config.yaml"]


# ---- runtime: server-dev (debuggable, debian-slim with full shell) ----------
FROM debian:bookworm-slim AS server-dev

LABEL org.opencontainers.image.title="code-agent-dev" \
      org.opencontainers.image.description="Code-Agent: dev image with shell for debugging" \
      org.opencontainers.image.source="https://github.com/spray272598/code-agent"

# Install debugging + healthcheck tools (not present in distroless)
RUN apt-get update \
 && apt-get install -y --no-install-recommends wget ca-certificates procps curl \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Binary + runtime assets
COPY --from=builder /out/server /app/server
COPY configs/config.example.yaml /app/configs/config.yaml
COPY scripts/sql/01_schema.sql /app/scripts/sql/01_schema.sql
COPY skills /app/skills
COPY teams /app/teams
COPY commands /app/commands
COPY hooks /app/hooks

# Create runtime directories (dev image has shell)
RUN mkdir -p /data/workspace /data/eino-checkpoints /data/objects

# Create non-root user (dev image)
RUN groupadd -r codeagent && useradd -r -g codeagent -d /app codeagent \
 && chown -R codeagent:codeagent /app /data

USER codeagent:codeagent
EXPOSE 8080

ENV LLM_USE_MOCK=true \
    DB_TYPE=memory \
    REDIS_ENABLED=false \
    OTLP_ENABLED=false \
    LOG_LEVEL=info \
    PATH=/app:$PATH

# Healthcheck: wget to HTTP health endpoint (distroless equivalent in compose)
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- -H "X-API-Key: dev-key" http://localhost:8080/health || exit 1

# Entrypoint allows overriding CMD when using 'docker run -it' for debugging
ENTRYPOINT ["/bin/bash", "-c"]
CMD ["exec /app/server -config /app/configs/config.yaml"]


# ---- runtime: cli (for interactive CLI access to server) -------------------
FROM gcr.io/distroless/static-debian12:nonroot AS cli

LABEL org.opencontainers.image.title="code-agent-cli" \
      org.opencontainers.image.description="Code-Agent: CLI tool for interacting with the server"

WORKDIR /app
COPY --from=builder /out/cli /app/cli

USER nonroot:nonroot
ENTRYPOINT ["/app/cli"]
CMD ["--base", "http://server:8080", "--key", "dev-key"]


# ---- runtime: host-agent (for local workspace tool execution) ----------------
FROM gcr.io/distroless/static-debian12:nonroot AS host-agent

LABEL org.opencontainers.image.title="code-agent-host" \
      org.opencontainers.image.description="Code-Agent: Host agent for local workspace tool execution"

WORKDIR /app
COPY --from=builder /out/host-agent /app/host-agent

USER nonroot:nonroot
ENTRYPOINT ["/app/host-agent"]


# Default target when no --target specified
FROM server