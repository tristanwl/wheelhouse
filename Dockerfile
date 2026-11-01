# syntax=docker/dockerfile:1

# Wheelhouse: the Go server with the Vite UI embedded in it, plus ffmpeg for the
# Codecs "Convert" feature. Base images are pinned by tag and digest (Dependabot
# keeps both current). The first two stages run on the build machine's own
# architecture and cross-compile, so `docker buildx build --platform
# linux/amd64,linux/arm64 .` does not emulate a compiler.

# 1. The UI: web/ -> web/dist (architecture independent).
FROM --platform=$BUILDPLATFORM node:24-alpine@sha256:44fce696f38ba8c987c4db89954051000bbd05ffd8d8659ba15ed4a8602e937f AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
# vite build only: `npm run build` also type-checks e2e/, which imports the
# acceptance suite (outside this image's context); CI runs the type check.
RUN npx vite build

# 2. The server: a static, CGO-free, path-independent binary with the UI
# embedded. The version comes from the root package.json (the single source of
# the release version) unless --build-arg VERSION=... is given.
FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS server
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=
WORKDIR /src
COPY package.json ./package.json
COPY server/go.mod server/go.sum* ./server/
WORKDIR /src/server
RUN go mod download
COPY server/ ./
COPY --from=web /src/web/dist/ ./internal/ui/dist/
RUN version="${VERSION:-$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' ../package.json | head -n 1)}" \
 && test -n "$version" \
 && CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath \
      -ldflags "-s -w -X github.com/tristanwl/wheelhouse/server/internal/version.Version=${version}" \
      -o /out/wheelhouse ./cmd/wheelhouse

# 3. The runtime image.
FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
LABEL org.opencontainers.image.title="Wheelhouse" \
      org.opencontainers.image.source="https://github.com/tristanwl/wheelhouse"
# ffmpeg/ffprobe power the Codecs "Convert" feature. VAAPI hardware encode (via
# /dev/dri) needs a driver for the GPU: mesa-va-gallium covers AMD and others;
# on x86 also libva-intel-driver (i965, Intel up to 8th gen, e.g. Haswell) and
# intel-media-driver (iHD, Broadwell and newer, including Quick Sync). Neither
# Intel driver exists for arm64. Wheelhouse tests a real encode at runtime and
# falls back to software x264/x265 when the host has no usable device.
RUN set -eu; \
    pkgs="ffmpeg mesa-va-gallium ca-certificates tzdata"; \
    if [ "$(apk --print-arch)" = x86_64 ]; then pkgs="$pkgs libva-intel-driver intel-media-driver"; fi; \
    apk add --no-cache $pkgs; \
    mkdir -p /config; chown 1000:1000 /config
COPY --from=server /out/wheelhouse /usr/local/bin/wheelhouse

# Everything Wheelhouse writes is under /config (settings.json) and the media
# directories (converted files and their .bak next to the original), so the root
# filesystem can be read-only. HOME is /tmp for the GPU driver caches, because an
# arbitrary --user has no home directory; mount a tmpfs there (--tmpfs /tmp).
ENV PORT=3050 \
    HOME=/tmp
# 1000:1000 by default. Override with --user UID:GID (any numeric ids, no entry
# in /etc/passwd needed) to match the owner of your /config and media volumes.
USER 1000:1000
EXPOSE 3050
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["wheelhouse", "healthcheck"]
ENTRYPOINT ["wheelhouse"]
