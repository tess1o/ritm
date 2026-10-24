# syntax=docker/dockerfile:1
# Multi-arch image (linux/amd64, linux/arm64): the UI and Go stages run on the build host and
# cross-compile, so no emulation is needed. Build with `make docker` or `make docker-multiarch`.

FROM --platform=$BUILDPLATFORM node:25-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build && npm run size

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
COPY migrations migrations
COPY web/embed.go web/embed.go
COPY --from=web /src/web/dist web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/ritm ./cmd/ritm \
 && mkdir -p /out/data && chmod 0700 /out/data

# distroless/static: no shell, no package manager; ships CA certificates, tzdata and a nonroot user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ritm /ritm
COPY --from=build --chown=65532:65532 /out/data /data
ENV RITM_DATA_DIR=/data RITM_LISTEN_ADDR=:8080
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s CMD ["/ritm", "healthcheck"]
ENTRYPOINT ["/ritm"]
CMD ["serve"]
