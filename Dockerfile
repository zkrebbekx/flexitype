# syntax=docker/dockerfile:1

# 1. Build the admin console (embedded into the Go binary).
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# 2. Build the statically linked Go binary with the console embedded.
FROM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
# The server is its own module (see cmd/flexitype/go.mod), so warm the cache
# from every module's manifest before copying the tree.
COPY go.mod go.sum ./
COPY cmd/flexitype/go.mod cmd/flexitype/go.sum ./cmd/flexitype/
COPY infrastructure/gcppubsub/go.mod infrastructure/gcppubsub/go.sum ./infrastructure/gcppubsub/
RUN go mod download && go mod download -C cmd/flexitype
COPY . .
# The console is embedded via web/embed.go (//go:embed all:dist); drop the
# freshly built assets in before compiling.
COPY --from=web /web/dist ./web/dist
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -C cmd/flexitype -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /flexitype .

# 3. Minimal runtime image.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /flexitype /flexitype
EXPOSE 8080
USER nonroot:nonroot

# The image serves /readyz and /healthz and declared no HEALTHCHECK, so an
# orchestrator that reads image metadata (Docker, Swarm, Compose) had nothing
# to check and treated a process that was up but not serving as healthy.
#
# /readyz rather than /healthz: readiness is what "can this instance take
# traffic" means — it reports the database as well as the process.
#
# The binary is the only executable in a distroless image, so the check calls
# it: `flexitype healthcheck` exits non-zero when /readyz does not answer 200.
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3     CMD ["/flexitype", "healthcheck"]

ENTRYPOINT ["/flexitype"]
