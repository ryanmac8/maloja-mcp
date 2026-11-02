# syntax=docker/dockerfile:1

# Build stage: static, CGO-free binary, cross-compiled for the target platform.
FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src

# Download modules in their own layer so it stays cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/maloja-mcp .

# Runtime stage: distroless static, non-root. No libc, so no glibc mismatch possible.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/maloja-mcp /usr/local/bin/maloja-mcp

ENV LISTEN_ADDR=:8080
EXPOSE 8080

HEALTHCHECK --interval=60s --timeout=10s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/maloja-mcp", "-healthcheck"]

ENTRYPOINT ["/usr/local/bin/maloja-mcp"]
