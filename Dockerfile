# HomeTwin in one small image: the front end built, embedded in the Go server, nothing else.
# Cross-compiled on the build machine, so the multi-arch build needs no emulation.
#
#   docker run -p 6417:6417 -v hometwin:/data ghcr.io/all-self-hosted/hometwin

FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS server
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web /src/web/dist/ ./web/dist/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -tags timetzdata -ldflags "-s -w -X main.version=$VERSION" -o /out/hometwin ./cmd/hometwin \
 && mkdir -p /out/data

FROM alpine:3.22
COPY --from=server /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=server /out/hometwin /usr/local/bin/hometwin
COPY --from=server --chown=1000:1000 /out/data /data
USER 1000:1000
VOLUME /data
EXPOSE 6417
ENTRYPOINT ["/usr/local/bin/hometwin"]
CMD ["-http", "0.0.0.0:6417", "-data", "/data"]
LABEL org.opencontainers.image.source="https://github.com/all-self-hosted/hometwin" \
      org.opencontainers.image.description="A live 3D twin of a home" \
      org.opencontainers.image.licenses="MIT"
