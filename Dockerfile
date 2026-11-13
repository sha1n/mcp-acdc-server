# Stage 1: Build
# Pinned to the build host's platform so the Go toolchain cross-compiles
# natively instead of running under QEMU emulation of the target platform.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Declared this late so every layer above is shared by all target platforms.
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /app/acdc-mcp ./cmd/acdc-mcp

# Stage 2: Runtime
FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/acdc-mcp .
# Install ca-certificates for external requests if needed
RUN apk --no-cache add ca-certificates

ENV ACDC_MCP_CONTENT_DIR=/app/content \
    ACDC_MCP_HOST=0.0.0.0 \
  ACDC_MCP_PORT=8080

EXPOSE 8080
CMD ["./acdc-mcp"]
