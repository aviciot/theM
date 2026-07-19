FROM golang:1.23-alpine AS builder

WORKDIR /build

# Install git — needed by go mod tidy for some indirect deps that use VCS.
RUN apk add --no-cache git

# Copy module manifest first for better layer caching. Run go mod tidy to
# generate a correct go.sum from the module file before source is present.
COPY go/go.mod ./
RUN go mod tidy

# Copy all Go source files and tidy again with full source present so
# test-only dependencies are included in go.sum, then run the full test suite.
COPY go/ ./
RUN go mod tidy && \
    go test ./... && \
    CGO_ENABLED=0 GOOS=linux go build -o /them ./cmd/them/

# ── Runtime image ──────────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=builder /them ./them

EXPOSE 8002

CMD ["./them"]
