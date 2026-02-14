ARG GO_BIN=agent
ARG HAS_INTERNAL=no
ARG HAS_DATA=no

# --- Base image with user setup ---
FROM golang:1.24-alpine AS prebuild

ENV USER=appuser
ENV UID=10001

RUN apk update && apk add --no-cache git ca-certificates \
    && adduser \
    --disabled-password \
    --gecos "" \
    --home "/nonexistent" \
    --shell "/sbin/nologin" \
    --no-create-home \
    --uid "${UID}" \
    "${USER}"

# --- Conditional internal/ directory ---
FROM prebuild AS build_yes
ONBUILD COPY internal/ /build/internal/

FROM prebuild AS build_no
ONBUILD RUN mkdir -p /build/internal

# --- Build stage ---
FROM build_${HAS_INTERNAL} AS build
ARG GO_BIN
COPY go.mod go.sum /build/
COPY cmd/ /build/cmd/
WORKDIR /build
RUN go mod download
RUN go mod verify
# Build only the requested binary (no CGO needed)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o /go/bin/app ./cmd/${GO_BIN}

# --- Conditional data/ directory ---
FROM build AS data_yes
ONBUILD COPY data/ /data/

FROM build AS data_no
ONBUILD RUN mkdir -p /data

FROM data_${HAS_DATA} AS runner

# --- Final minimal image ---
# NOTE: Using alpine instead of scratch because docker-compose commands
# use shell for log piping (sh -c "... | tee ...")
FROM alpine:3.21

RUN apk add --no-cache ca-certificates

RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/nonexistent" \
    --shell "/sbin/nologin" \
    --no-create-home \
    --uid 10001 \
    appuser

WORKDIR /app

COPY --from=runner /go/bin/app /app/bin/app
COPY --from=runner /data /app/data
COPY config/ /app/config/

RUN mkdir -p /app/data && chown -R appuser:appuser /app

USER appuser:appuser

EXPOSE 8080

CMD ["/app/bin/app", "--config", "/app/config/agent.yaml"]
