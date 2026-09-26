# agentstats backend image: the whole backend (doc 12) in one Go binary.
# Runs in a Cloudflare Container (linux/amd64) behind the front Worker. It bare-clones the
# user's repos with the user token, counts, renders every card/badge, and writes them to R2
# through the Worker's /_internal/r2 route. Code and tokens never persist beyond a job.

# ---- build: cross-compile natively (no emulation) ----
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/analyzer ./cmd/analyzer

# ---- runtime ----
FROM --platform=linux/amd64 debian:stable-slim
ARG RESVG_VERSION=0.48.1
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates curl fonts-inter \
 && curl -fsSL "https://github.com/linebender/resvg/releases/download/v${RESVG_VERSION}/resvg-linux-x86_64.tar.gz" \
    | tar -xz -C /usr/local/bin resvg \
 && chmod +x /usr/local/bin/resvg \
 && apt-get purge -y curl && apt-get autoremove -y \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/server /usr/local/bin/server
COPY --from=build /out/analyzer /usr/local/bin/analyzer
ENV GIT_TERMINAL_PROMPT=0 ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server"]
