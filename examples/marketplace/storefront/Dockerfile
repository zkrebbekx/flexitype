# Build context is the repository root: this module `replace`s the core module
# (for pkg/events, the webhook signature scheme) and the client SDK with the
# checkout beside it.
FROM golang:1.25-alpine AS build
WORKDIR /src
# Warm the module cache from the manifests of every module in the replace
# graph, before the source is copied, so a source-only change reuses the layer.
COPY go.mod go.sum ./
COPY client/go.mod ./client/
COPY examples/marketplace/storefront/go.mod examples/marketplace/storefront/go.sum ./examples/marketplace/storefront/
RUN go mod download -C examples/marketplace/storefront
COPY . .
# GOWORK=off: go.work is copied in with the tree, and a workspace build would
# resolve modules through it rather than through this module's replaces, which
# is not what a published image should depend on.
ENV GOWORK=off
RUN CGO_ENABLED=0 go build -C examples/marketplace/storefront -trimpath -ldflags "-s -w" -o /storefront .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /storefront /storefront
EXPOSE 9200
USER nonroot:nonroot
ENTRYPOINT ["/storefront"]
