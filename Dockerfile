FROM golang:1.24-alpine AS builder

WORKDIR /app

# Download dependencies first for better layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /url-shortener ./cmd/api

# ---- Runtime stage ----
FROM alpine:3.20

# Install ca-certificates for HTTPS outbound requests (if needed)
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy binary from builder
COPY --from=builder /url-shortener /app/url-shortener

# Create data directory for SQLite
RUN mkdir -p /app/data

# Expose the default port
EXPOSE 8080

# Run as non-root user
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
RUN chown -R appuser:appgroup /app
USER appuser

ENTRYPOINT ["/app/url-shortener"]
