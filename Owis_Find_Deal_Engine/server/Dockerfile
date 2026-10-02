# syntax=docker/dockerfile:1

# Build: static Go binary (no cgo), reproducible paths, debug info stripped.
ARG GO_IMAGE=golang:1.26-alpine
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/owis_find_deal_engine ./cmd/owis_find_deal_engine

# Run: distroless = no shell, no package manager; CA certificates included
# (for SerpApi and the auth provider's JWKS over HTTPS). Runs as non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/owis_find_deal_engine /owis_find_deal_engine
USER nonroot:nonroot
ENV PORT=3002
EXPOSE 3002
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/owis_find_deal_engine", "healthcheck"]
ENTRYPOINT ["/owis_find_deal_engine"]
