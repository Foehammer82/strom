FROM golang:1.26 AS build
WORKDIR /src

COPY controller/go.mod ./controller/go.mod
COPY controller/go.sum ./controller/go.sum
COPY controller/internal ./controller/internal
COPY controller/cmd/controller ./controller/cmd/controller

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