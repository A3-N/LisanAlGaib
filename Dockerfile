# syntax=docker/dockerfile:1.7

FROM golang:1.26-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599 AS heighliner

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o /out/lisan ./cmd/lisan

# Agent download tooling never enters the final image. Each payload is fetched
# only when its profile selection is enabled.
FROM ubuntu:26.04@sha256:678c6550cc43645e08669028bc177f50be4e7c5b8cca677067b1914d4afc7a03 AS guild_navigator

ARG TARGETARCH
ARG LISAN_INSTALL_CODEX=0
ARG LISAN_INSTALL_OPENCODE=0
ARG LISAN_INSTALL_CLAUDE=0
ARG LISAN_INSTALL_KIMI=0
ARG CODEX_VERSION=0.145.0
ARG CODEX_SHA256_AMD64=bfaf13c9ba34f2ad764e4a916c49cf7177aeba329cf0f719e2227566fc8d662a
ARG CODEX_SHA256_ARM64=d384f90bc842450b42bd675feef06a12a46a3b1ca97efcb22566b270e4a11227
ARG OPENCODE_VERSION=1.18.14
ARG OPENCODE_SHA256_AMD64=f23980ba2aebfbfa53948e55e213d3f2a53740fd7326553828e89ad27e970572
ARG OPENCODE_SHA256_ARM64=27ede7aa2080002459d8c970a40016bbef49cd13bb467302777da67467f1602d
ARG KIMI_VERSION=0.34.0
ARG KIMI_SHA256_AMD64=1e05b9b78c4fc69abb7f9c3ade7e6e774daa87cc4a31773cdbd72f047d2e732e
ARG KIMI_SHA256_ARM64=db9c88d0f44420f1245cf745eadb569de18ecd83019ecab888b3028eddf36e87
ARG CLAUDE_VERSION=2.1.223
ARG CLAUDE_SHA256_AMD64=98226474f802e3094d6a86c5ade8883c16206d0fcb5c400b7401c800063e99d7
ARG CLAUDE_SHA256_ARM64=60e83d8db0e894d0e54413e5e7daa256d180db660f51e139a51b614fc30cf3ac

RUN set -eu; \
    mkdir -p /opt/lisan-agents/bin; \
    if [ "$LISAN_INSTALL_CODEX$LISAN_INSTALL_OPENCODE$LISAN_INSTALL_CLAUDE$LISAN_INSTALL_KIMI" != "0000" ]; then \
      download_packages="ca-certificates curl"; \
      if [ "$LISAN_INSTALL_CODEX$LISAN_INSTALL_OPENCODE" != "00" ]; then \
        download_packages="$download_packages tar"; \
      fi; \
      apt-get update; \
      apt-get install -y --no-install-recommends $download_packages; \
      rm -rf /var/lib/apt/lists/*; \
    fi; \
    temp_dir="$(mktemp -d)"; \
    trap 'rm -rf "$temp_dir"' EXIT; \
    case "$TARGETARCH" in \
      amd64) \
        agent_arch=x64; \
        codex_target=x86_64; \
        codex_sha="$CODEX_SHA256_AMD64"; \
        opencode_sha="$OPENCODE_SHA256_AMD64"; \
        kimi_sha="$KIMI_SHA256_AMD64"; \
        claude_sha="$CLAUDE_SHA256_AMD64"; \
        ;; \
      arm64) \
        agent_arch=arm64; \
        codex_target=aarch64; \
        codex_sha="$CODEX_SHA256_ARM64"; \
        opencode_sha="$OPENCODE_SHA256_ARM64"; \
        kimi_sha="$KIMI_SHA256_ARM64"; \
        claude_sha="$CLAUDE_SHA256_ARM64"; \
        ;; \
      *) echo "Unsupported Docker architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    if [ "$LISAN_INSTALL_CODEX" = 1 ]; then \
      codex_archive="codex-${codex_target}-unknown-linux-musl.tar.gz"; \
      curl -fsSL -o "$temp_dir/$codex_archive" "https://github.com/openai/codex/releases/download/rust-v${CODEX_VERSION}/${codex_archive}"; \
      printf '%s  %s\n' "$codex_sha" "$temp_dir/$codex_archive" | sha256sum -c -; \
      tar -xzf "$temp_dir/$codex_archive" -C "$temp_dir"; \
      install -m 0755 "$temp_dir/codex-${codex_target}-unknown-linux-musl" /opt/lisan-agents/bin/codex; \
    fi; \
    if [ "$LISAN_INSTALL_OPENCODE" = 1 ]; then \
      curl -fsSL -o "$temp_dir/opencode.tar.gz" "https://github.com/anomalyco/opencode/releases/download/v${OPENCODE_VERSION}/opencode-linux-${agent_arch}.tar.gz"; \
      printf '%s  %s\n' "$opencode_sha" "$temp_dir/opencode.tar.gz" | sha256sum -c -; \
      tar -xzf "$temp_dir/opencode.tar.gz" -C "$temp_dir"; \
      install -m 0755 "$temp_dir/opencode" /opt/lisan-agents/bin/opencode; \
    fi; \
    if [ "$LISAN_INSTALL_KIMI" = 1 ]; then \
      kimi_file="kimi-code-linux-${agent_arch}"; \
      curl -fsSL -o "$temp_dir/kimi" "https://code.kimi.com/kimi-code/binaries/${KIMI_VERSION}/${kimi_file}"; \
      printf '%s  %s\n' "$kimi_sha" "$temp_dir/kimi" | sha256sum -c -; \
      install -m 0755 "$temp_dir/kimi" /opt/lisan-agents/bin/kimi; \
    fi; \
    if [ "$LISAN_INSTALL_CLAUDE" = 1 ]; then \
      curl -fsSL -o "$temp_dir/claude" "https://downloads.claude.ai/claude-code-releases/${CLAUDE_VERSION}/linux-${agent_arch}/claude"; \
      printf '%s  %s\n' "$claude_sha" "$temp_dir/claude" | sha256sum -c -; \
      install -m 0755 "$temp_dir/claude" /opt/lisan-agents/bin/claude; \
    fi

FROM ubuntu:26.04@sha256:678c6550cc43645e08669028bc177f50be4e7c5b8cca677067b1914d4afc7a03 AS sietch_tabr

ENV DEBIAN_FRONTEND=noninteractive \
    HOME=/home/fremen \
    USER=fremen \
    LOGNAME=fremen \
    TERM=xterm-256color \
    COLORTERM=truecolor \
    PATH=/opt/lisan-agents/bin:/home/fremen/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# This stable base is intentionally independent of the active profile. Docker
# can keep it cached while the smaller selected-package layer below changes.
RUN apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates sudo; \
    rm -rf /var/lib/apt/lists/*

ENV LANG=C.UTF-8 LC_ALL=C.UTF-8

RUN existing_user="$(getent passwd 1000 | cut -d: -f1)"; \
    if [ -n "$existing_user" ]; then userdel -r "$existing_user" || userdel "$existing_user"; fi; \
    existing_group="$(getent group 1000 | cut -d: -f1)"; \
    if [ -n "$existing_group" ]; then groupdel "$existing_group"; fi; \
    groupadd --gid 1000 fremen; \
    useradd --create-home --uid 1000 --gid fremen --shell /bin/sh --groups sudo fremen; \
    passwd --lock root; \
    passwd --lock fremen; \
    printf 'fremen ALL=(ALL:ALL) NOPASSWD: ALL\n' > /etc/sudoers.d/fremen; \
    chmod 0440 /etc/sudoers.d/fremen

# Profile-dependent apt work stays after the stable operating-system setup.
# Changing one profile therefore reuses the heavyweight base layers.
ARG LISAN_DOCKER_SHELL=fish
ARG LISAN_SHELL_PATH=/usr/bin/fish
ARG LISAN_INSTALL_GIT=0
ARG LISAN_INSTALL_RG=0
ARG LISAN_INSTALL_NVIM=0
ARG LISAN_INSTALL_NVCHAD=0
ARG LISAN_INSTALL_NODE=0
ARG LISAN_INSTALL_GO=0
ARG LISAN_INSTALL_PYTHON=0

RUN set -eu; \
    case "$LISAN_DOCKER_SHELL:$LISAN_SHELL_PATH" in \
      fish:/usr/bin/fish|bash:/usr/bin/bash|zsh:/usr/bin/zsh|sh:/bin/sh) ;; \
      *) echo "Unsupported Docker shell: $LISAN_DOCKER_SHELL ($LISAN_SHELL_PATH)" >&2; exit 1 ;; \
    esac; \
    packages=""; \
    if [ "$LISAN_DOCKER_SHELL" != sh ]; then packages="$LISAN_DOCKER_SHELL"; fi; \
    if [ "$LISAN_INSTALL_GIT" = 1 ]; then packages="$packages git openssh-client"; fi; \
    if [ "$LISAN_INSTALL_RG" = 1 ]; then packages="$packages ripgrep"; fi; \
    if [ "$LISAN_INSTALL_NVIM" = 1 ]; then packages="$packages neovim"; fi; \
    if [ "$LISAN_INSTALL_NVCHAD" = 1 ]; then packages="$packages fd-find unzip"; fi; \
    if [ "$LISAN_INSTALL_NODE" = 1 ]; then packages="$packages nodejs npm"; fi; \
    if [ "$LISAN_INSTALL_GO" = 1 ]; then packages="$packages golang-go"; fi; \
    if [ "$LISAN_INSTALL_PYTHON" = 1 ]; then packages="$packages python3 python3-venv"; fi; \
    if [ -n "$packages" ]; then \
      apt-get update; \
      apt-get install -y --no-install-recommends $packages; \
    fi; \
    if command -v fdfind >/dev/null 2>&1; then ln -s /usr/bin/fdfind /usr/local/bin/fd; fi; \
    rm -rf /var/lib/apt/lists/*; \
    usermod --shell "$LISAN_SHELL_PATH" fremen

ENV SHELL=${LISAN_SHELL_PATH} \
    LISAN_NVCHAD=${LISAN_INSTALL_NVCHAD}

COPY --from=heighliner /out/lisan /usr/local/bin/lisan
COPY --from=guild_navigator /opt/lisan-agents /opt/lisan-agents
COPY docker/lisan-entrypoint /usr/local/bin/lisan-entrypoint
COPY docker/nvim /usr/local/share/lisan/nvim
COPY docker/home /usr/local/share/lisan/home
RUN chmod 0755 /usr/local/bin/lisan /usr/local/bin/lisan-entrypoint; \
    cp -a /usr/local/share/lisan/home/. /home/fremen/; \
    if [ "$LISAN_INSTALL_NVCHAD" = 1 ]; then \
      mkdir -p /home/fremen/.config/nvim; \
      cp -a /usr/local/share/lisan/nvim/. /home/fremen/.config/nvim/; \
    fi; \
    chown -R fremen:fremen /home/fremen /usr/local/share/lisan/home /usr/local/share/lisan/nvim

USER fremen
WORKDIR /home/fremen
RUN if [ "$LISAN_INSTALL_NVCHAD" = 1 ]; then nvim --headless '+Lazy! install' +qa; fi

# The fingerprint is metadata-only and deliberately last: source/profile
# changes cannot invalidate the package-install layers through this label.
ARG LISAN_BUILD_SIGNATURE=manual
LABEL io.lisanalgaib.build-signature="$LISAN_BUILD_SIGNATURE"

ENTRYPOINT ["/usr/local/bin/lisan-entrypoint"]
CMD ["sleep", "infinity"]
