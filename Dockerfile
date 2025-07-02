# Multi-stage build for anantha
FROM golang:1.24-bookworm AS builder

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the anantha binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o anantha ./cmd/anantha

# Runtime stage
FROM debian:bookworm-slim

# Install ca-certificates for HTTPS connections
RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

# Create non-root user
RUN useradd -r -s /bin/false anantha

# Set working directory
WORKDIR /app

# Copy the binary from builder stage
COPY --from=builder /app/anantha .

# Change ownership to non-root user
RUN chown -R anantha:anantha /app

# Switch to non-root user
USER anantha

# Expose ports
# 53: DNS server
# 80: HTTP server
# 443: HTTPS server
# 8883: MQTT broker
# 26268: Web dashboard
EXPOSE 53/udp 53/tcp 80 443 8883 26268

# Set the entrypoint
ENTRYPOINT ["./anantha"]

# Default command
CMD ["serve", "--help"]
