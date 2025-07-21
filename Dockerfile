# Use official Go image for building
FROM golang:1.18 AS builder

# Set working directory
WORKDIR /app

# Copy go.mod and go.sum for dependency caching
COPY go.mod go.sum ./

# Install dependencies
RUN go mod download

# Copy source files
COPY server/ ./server/
COPY config/ ./config/
COPY proto/ ./proto/
COPY buf.yaml buf.gen.yaml ./

# Install Buf CLI
RUN curl -sSL https://github.com/bufbuild/buf/releases/latest/download/buf-Linux-x86_64 -o /usr/local/bin/buf && \
    chmod +x /usr/local/bin/buf

# Generate Protobuf code
RUN buf mod update && buf generate

# Build server
RUN go build -o /app/server ./server

# Use minimal image for runtime
FROM gcr.io/distroless/base-debian11

# Copy binary and config
COPY --from=builder /app/server /app/server
COPY config.yaml /app/config.yaml

# Expose ports
EXPOSE 50051 8080

# Run server
CMD ["/app/server", "-config=/app/config.yaml"]