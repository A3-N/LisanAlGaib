# syntax=docker/dockerfile:1.7

FROM golang:1.26-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599 AS heighliner

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/extension-host ./cmd/extension-host
COPY internal/appconfig ./internal/appconfig
COPY internal/childproc ./internal/childproc
COPY internal/connectors ./internal/connectors
COPY internal/extensionhost ./internal/extensionhost
COPY internal/lifecycle ./internal/lifecycle
COPY internal/safefile ./internal/safefile
COPY internal/textsafe ./internal/textsafe
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /out/extension-host ./cmd/extension-host

FROM ubuntu:26.04@sha256:678c6550cc43645e08669028bc177f50be4e7c5b8cca677067b1914d4afc7a03

ENV DEBIAN_FRONTEND=noninteractive \
    HOME=/home/ixian \
    PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates \
      curl && \
    groupadd --gid 10001 ixian && \
    useradd --create-home --uid 10001 --gid ixian --shell /usr/sbin/nologin ixian && \
    rm -rf /var/lib/apt/lists/*

COPY --from=heighliner /out/extension-host /usr/local/bin/extension-host
COPY --chmod=0444 docker/connectors/ixian-proving-ground/extension.json /etc/lisan/extension.json
COPY --chmod=0555 docker/connectors/ixian-proving-ground/ixian-showcase /usr/local/bin/ixian-showcase

USER ixian
WORKDIR /home/ixian
EXPOSE 7777
HEALTHCHECK --interval=20s --timeout=3s --start-period=5s --retries=3 \
  CMD curl -fsS http://127.0.0.1:7777/v1/health || exit 1

ENTRYPOINT ["/usr/local/bin/extension-host", "--config", "/etc/lisan/extension.json"]
