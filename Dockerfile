# ==========================================
# STAGE 1: Build
# ==========================================
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

# Install CA certificates and minimal build tools.
RUN apk add --no-cache git ca-certificates tzdata

# Set the working directory inside the container.
WORKDIR /app

# Download dependencies first to use Docker layer caching.
COPY go.mod go.sum ./
RUN go mod download

# Copy the remaining source code.
COPY . .

ARG TARGETOS
ARG TARGETARCH

# Build the Go binary with production optimizations:
# - CGO_ENABLED=0: Disable CGO to produce a self-contained static binary.
# - GOOS/GOARCH: Cross-compile for the requested target platform.
# - ldflags="-s -w": Remove the symbol table and debugging information to reduce binary size.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -ldflags="-s -w" \
    -o kube-dra-exporter .

# ==========================================
# STAGE 2: Runtime image
# ==========================================
# Use a static distroless image without a shell, package manager, or redundant tools.
# This reduces the attack surface and exposure to vulnerabilities (CVEs).
FROM gcr.io/distroless/static-debian13:latest

# Copy time zone data and certificates from the builder.
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Set default environment variables (for example, the UTC time zone).
ENV TZ=UTC

# Copy only the compiled binary from the previous stage.
COPY --from=builder /app/kube-dra-exporter /kube-dra-exporter

# Include the project's license and attribution in the distributed image.
COPY LICENSE NOTICE /licenses/kube-dra-exporter/

# Use the built-in distroless non-root user for security (ID: 65532).
USER 65532:65532

# Expose the port configured in the Go code.
EXPOSE 8080

# Run the exporter.
ENTRYPOINT ["/kube-dra-exporter"]
