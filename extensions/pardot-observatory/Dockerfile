# syntax=docker/dockerfile:1.7

FROM golang:1.26.5-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599 AS heighliner
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/connectors ./internal/connectors
COPY internal/extensionhost ./internal/extensionhost
COPY internal/appconfig ./internal/appconfig
COPY internal/safefile ./internal/safefile
COPY internal/textsafe ./internal/textsafe
COPY extensions/pardot-observatory/cmd ./extensions/pardot-observatory/cmd
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /out/pardot-observatory ./extensions/pardot-observatory/cmd/pardot-observatory

FROM scratch
COPY --from=heighliner --chown=65532:65532 /out/pardot-observatory /pardot-observatory
COPY --chown=65532:65532 extensions/pardot-observatory/state.seed /var/lib/lisan-extension/.seed
USER 65532:65532
EXPOSE 7777
ENTRYPOINT ["/pardot-observatory", "--listen", "0.0.0.0:7777"]
