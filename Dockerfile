# syntax=docker/dockerfile:1
# Web IP Phone: static Go binary with the embedded web UI on a distroless, non-root base.
# Base images are pinned by digest (Dependabot keeps them current).

FROM --platform=$BUILDPLATFORM node:26-alpine@sha256:0b36e8c136b94cd4fcf02188228e76c31ad5872eef3fec8cbd2eee500cfd9e80 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/
COPY --from=web /src/web/dist web/dist
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/webphone ./cmd/webphone \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
LABEL org.opencontainers.image.title="Web IP Phone" \
      org.opencontainers.image.description="Use the extensions of your internal PBXs from anywhere: web + Android softphone gateway" \
      org.opencontainers.image.source="https://github.com/revocx35/web-ip-phone" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/webphone /usr/local/bin/webphone
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
ENV WEBPHONE_DATA_DIR=/data
VOLUME ["/data"]
EXPOSE 8443/tcp 5070/udp 5070/tcp
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 CMD ["/usr/local/bin/webphone", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/webphone"]
