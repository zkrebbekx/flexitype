# Build context is the repository root: this module `replace`s the core module
# and the client SDK with the checkout beside it.
FROM golang:1.25-alpine AS build
WORKDIR /src
# Warm the module cache from the manifests before the source is copied, so a
# source-only change reuses the layer.
COPY go.mod go.sum ./
COPY client/go.mod ./client/
COPY examples/kitchen/api/go.mod examples/kitchen/api/go.sum ./examples/kitchen/api/
RUN go mod download -C examples/kitchen/api
COPY . .
# GOWORK=off: go.work is copied in with the tree, and a workspace build would
# resolve modules through it rather than through this module's replaces, which
# is not what a published image should depend on.
ENV GOWORK=off
RUN CGO_ENABLED=0 go build -C examples/kitchen/api -trimpath -ldflags "-s -w" -o /kitchen .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /kitchen /kitchen
EXPOSE 9400
USER nonroot:nonroot
ENTRYPOINT ["/kitchen"]
