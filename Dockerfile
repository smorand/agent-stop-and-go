# Stage 1: Build
FROM golang:1.23-bookworm AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO is needed for sqlite3
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o /app/bin/agent ./cmd/agent
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o /app/bin/mcp-resources ./cmd/mcp-resources
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o /app/bin/web ./cmd/web

# Stage 2: Runtime
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*

# Create non-root user
RUN groupadd -r appuser && useradd -r -g appuser -d /app -s /sbin/nologin appuser

WORKDIR /app

COPY --from=builder /app/bin/agent /app/bin/agent
COPY --from=builder /app/bin/mcp-resources /app/bin/mcp-resources
COPY --from=builder /app/bin/web /app/bin/web
COPY config/ /app/config/

RUN mkdir -p /app/data && chown -R appuser:appuser /app

USER appuser

EXPOSE 8080

CMD ["/app/bin/agent", "--config", "/app/config/agent.yaml"]
