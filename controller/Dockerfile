FROM node:22-bookworm-slim AS web-build
WORKDIR /src/controller/web

COPY controller/web/package.json ./package.json
COPY controller/web/tsconfig.json ./tsconfig.json
COPY controller/web/tsconfig.app.json ./tsconfig.app.json
COPY controller/web/tsconfig.node.json ./tsconfig.node.json
COPY controller/web/vite.config.ts ./vite.config.ts
COPY controller/web/index.html ./index.html
COPY controller/web/src ./src

RUN npm install && npm run build

FROM golang:1.26 AS build
WORKDIR /src

COPY controller/go.mod ./controller/go.mod
COPY controller/go.sum ./controller/go.sum
COPY controller/internal ./controller/internal
COPY controller/cmd/controller ./controller/cmd/controller
COPY --from=web-build /src/controller/web/dist ./controller/cmd/controller/assets

WORKDIR /src/controller
ARG VERSION=dev
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build -ldflags "-X main.version=${VERSION}" -o /out/wattkeeper-controller ./cmd/controller

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/wattkeeper-controller /app/wattkeeper-controller
VOLUME ["/data"]
EXPOSE 9000
ENTRYPOINT ["/app/wattkeeper-controller"]
CMD ["--data-dir=/data", "--listen=:9000"]
