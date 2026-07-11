# Build context is the repo root (the consumer imports pkg/events).
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" \
    -o /consumer ./examples/catalog/consumer

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /consumer /consumer
EXPOSE 9100
USER nonroot:nonroot
ENTRYPOINT ["/consumer"]
