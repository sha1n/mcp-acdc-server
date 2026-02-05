# Stage 1: Build
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/acdc-mcp ./cmd/acdc-mcp

# Stage 2: Runtime
FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/acdc-mcp .
# Install ca-certificates for external requests if needed
RUN apk --no-cache add ca-certificates

ENV ACDC_MCP_CONFIG=/app/content/mcp-metadata.yaml \
    ACDC_MCP_HOST=0.0.0.0 \
    ACDC_MCP_PORT=8080

EXPOSE 8080
CMD ["./acdc-mcp"]
