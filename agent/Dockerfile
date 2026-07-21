FROM golang:1.26 AS build
WORKDIR /src

COPY agent/go.mod ./agent/go.mod
COPY agent/go.sum ./agent/go.sum
COPY agent/cmd ./agent/cmd
COPY agent/internal ./agent/internal
COPY agent/nodeapi ./agent/nodeapi

WORKDIR /src/agent
ARG VERSION=dev
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build -ldflags "-X main.version=${VERSION}" -o /out/strom-agent ./cmd/agent

FROM debian:bookworm-slim
WORKDIR /app

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates nut \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/strom-agent /app/strom-agent
COPY agent/docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh

EXPOSE 80 8443 3493
ENTRYPOINT ["/app/docker-entrypoint.sh"]
