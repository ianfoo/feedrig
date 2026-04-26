# syntax=docker/dockerfile:1.7
#
# Multi-stage build for feedrig. The final image carries the static binary,
# yt-dlp, ffmpeg, and chromium — everything the runtime needs.
#
# Build:   docker build -t feedrig .
# Run:     docker run -p 7777:7777 -v feedrig-data:/data -v feedrig-media:/media feedrig
# Cron a sweep:  docker run --rm -v feedrig-data:/data feedrig sweep
# MCP via stdio: docker run -i --rm -v feedrig-data:/data feedrig mcp
#

# --- Stage 1: build the Go binary ---------------------------------
FROM golang:1.24-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Static-ish build: pure Go SQLite means no CGO required.
ENV CGO_ENABLED=0 GOFLAGS="-trimpath"
RUN go build -ldflags="-s -w" -o /out/feedrig .

# --- Stage 2: runtime --------------------------------------------
FROM debian:bookworm-slim AS runtime

# yt-dlp, ffmpeg for ingestion + transcription preprocessing.
# chromium for the chromedp discoverer.
# ca-certificates for HTTPS to Instagram, Ollama, OpenRouter, S3.
# tini as PID 1 so signals propagate to the child server cleanly.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates tini yt-dlp ffmpeg chromium \
    && rm -rf /var/lib/apt/lists/*

# Non-root user with stable uid for volume permissions.
RUN useradd --system --uid 1000 --create-home --home-dir /home/feedrig feedrig

WORKDIR /home/feedrig
COPY --from=build /out/feedrig /usr/local/bin/feedrig

# Volumes for state. The defaults match the binary's flag defaults so
# `docker run -v ... -v ...` works without extra wiring.
RUN mkdir -p /data /media && chown -R feedrig:feedrig /data /media
VOLUME ["/data", "/media"]

USER feedrig
EXPOSE 7777

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/feedrig"]
CMD ["-addr", "0.0.0.0:7777", "-data", "/data", "-media", "/media"]
