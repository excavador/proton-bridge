# Proton Mail Bridge, headless, built from upstream source.
#
# WHY THIS EXISTS. Proton publishes no container image and does not support
# Docker or Kubernetes. Every containerised Bridge is therefore a repack. The
# most-used community image was eight releases behind upstream at the time this
# was written, and its `latest` tag is amd64-only -- which rules it out of any
# arm64 cluster. Bridge decrypts every message and holds the Proton session, so
# "current" and "built from a source we pinned ourselves" are not luxuries.
#
# Headless is upstream-supported: `build-nogui` is a first-class target in
# Proton's own Makefile, declared in its .PHONY list. This image uses it. It
# does not patch Bridge, and it does not vendor a fork.
ARG BRIDGE_VERSION=v3.27.0

FROM golang:1.26-bookworm AS build
ARG BRIDGE_VERSION

# CGO_ENABLED=1 is exported by Bridge's Makefile, and the linux build links
# -lfido2 -lcbor -lssl -lcrypto for FIDO2 security keys. These are build-time
# headers; the runtime stage installs the matching shared libraries.
# libsecret-1-dev is NOT optional even though this image uses `pass` at
# runtime: Bridge compiles in the docker-credential-helpers secretservice
# backend unconditionally, so its headers are needed to build at all. Omitting
# it fails with "Package 'libsecret-1' ... not found" from pkg-config.
RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential pkg-config git ca-certificates \
        libfido2-dev libcbor-dev libssl-dev libsecret-1-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
# A tag, never a branch. `utils/get_revision.sh` shells out to git for the
# version it stamps into the binary, so the tag object has to be in the
# checkout -- which is why this is a git clone of one tag rather than a
# source tarball.
RUN git clone --depth 1 --branch "${BRIDGE_VERSION}" \
        https://github.com/ProtonMail/proton-bridge.git . \
    && git describe --tags --always

# build-nogui produces TWO binaries and the names mislead:
#   bridge         -- the application itself (~46 MB). This is what we ship.
#   proton-bridge  -- the LAUNCHER (~14 MB), which execs a GUI binary and
#                     handles desktop self-update. In a container it is wrong
#                     twice over: there is no GUI to find (it dies with
#                     "No executable in launcher directory ... exe_to_launch=
#                     bridge-gui") and self-update has no business in an image
#                     whose version is pinned by its tag.
RUN make build-nogui && ./bridge --version

FROM debian:bookworm-slim
ARG BRIDGE_VERSION
LABEL org.opencontainers.image.title="Proton Mail Bridge (headless)" \
      org.opencontainers.image.description="Proton Mail Bridge built from upstream source with make build-nogui, for headless and Kubernetes use." \
      org.opencontainers.image.source="https://github.com/excavador/proton-bridge" \
      org.opencontainers.image.version="${BRIDGE_VERSION}" \
      org.opencontainers.image.licenses="GPL-3.0-only"

# `pass` is Bridge's OWN preferred Linux keychain, not a workaround for
# containers: pkg/keychain/helper_linux.go probes for it with LookPath and
# makes it the default helper when found, falling back to secret-service only
# when it is absent. gnupg is what pass encrypts with. socat moves the
# listeners off localhost -- see the entrypoint.
# libsecret-1-0 ships here for the same reason its headers ship above: the
# secretservice backend is linked in, so the shared library must exist even
# though `pass` is the helper actually used.
RUN apt-get update && apt-get install -y --no-install-recommends \
        pass gnupg socat ca-certificates \
        libfido2-1 libcbor0.8 libssl3 libsecret-1-0 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /src/bridge /usr/local/bin/proton-bridge
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 0755 /usr/local/bin/entrypoint.sh

# Bridge keeps its keychain and message cache under $HOME. Mount a volume here
# or every restart costs a login at a 2FA prompt -- and a fresh login mints new
# local IMAP credentials, so it costs a secrets update too.
VOLUME ["/root"]
EXPOSE 143 25

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
