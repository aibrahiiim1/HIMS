# Multi-stage build for hims-api. Produces a small static image.
#
#   docker build -f deploy/docker/Dockerfile -t hims-api:latest .
# (build context must be the repo root so the Go module is visible.)

FROM golang:1.26-alpine AS build
WORKDIR /src
# Cache modules first.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO off → fully static binary that runs on a scratch/alpine base.
ARG VERSION=docker
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/hims-api ./cmd/hims-api \
 && CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" \
      -o /out/hims-migrate ./cmd/hims-migrate

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget && addgroup -S hims && adduser -S -G hims hims
WORKDIR /opt/hims
COPY --from=build /out/hims-api /out/hims-migrate /opt/hims/
# Optional: stage prebuilt relay-agent binaries here (or mount a volume).
#   COPY dist/agents /opt/hims/agents
ENV HIMS_ADDR=:8090 HIMS_SERVICE_MODE=docker HIMS_AGENT_DIST_DIR=/opt/hims/agents
EXPOSE 8090
USER hims
# Container healthcheck mirrors what the orchestrator/System Health checks.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -qO- http://localhost:8090/healthz >/dev/null 2>&1 || exit 1
ENTRYPOINT ["/opt/hims/hims-api"]
