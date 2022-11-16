# syntax = docker/dockerfile:1.4

# Dependencies
ARG RUNC_VERSION=v1.1.4
ARG ROOTLESSKIT_VERSION=v1.1.0
ARG SLIRP4NETNS_VERSION=v1.2.0

# Images
ARG GO_VERSION=1.19

# build buildg
FROM --platform=${BUILDPLATFORM} golang:${GO_VERSION}-bullseye AS base

FROM base AS build-buildg
ARG TARGETARCH
ENV GOARCH=${TARGETARCH:-amd64}
COPY . /go/src/github.com/ktock/buildg
WORKDIR /go/src/github.com/ktock/buildg
RUN --mount=type=cache,target=/root/.cache/go-build \
  --mount=type=cache,target=/go/pkg/mod \
  PREFIX=/out/ make

# build runc
# Adopted from https://github.com/containerd/nerdctl/blob/v0.22.2/Dockerfile#L75
# Apache License 2.0
FROM base AS build-runc
ARG RUNC_VERSION
RUN dpkg --add-architecture arm64 && \
  dpkg --add-architecture amd64 && \
  apt-get update && \
  apt-get install -y crossbuild-essential-amd64 crossbuild-essential-arm64 libseccomp-dev:amd64 libseccomp-dev:arm64
RUN git clone https://github.com/opencontainers/runc.git /go/src/github.com/opencontainers/runc
WORKDIR /go/src/github.com/opencontainers/runc
RUN git checkout ${RUNC_VERSION} && \
  mkdir -p /out
ENV CGO_ENABLED=1
RUN GOARCH=amd64 CC=x86_64-linux-gnu-gcc make static && \
  cp -a runc /out/runc.amd64
RUN GOARCH=arm64 CC=aarch64-linux-gnu-gcc make static && \
  cp -a runc /out/runc.arm64

# full artifacts
# Adopted from https://github.com/containerd/nerdctl/blob/v0.22.2/Dockerfile#L108
# Apache License 2.0
FROM base AS build-full
ARG TARGETARCH
ARG RUNC_VERSION
COPY . /go/src/github.com/ktock/buildg
COPY ./Dockerfile.d/SHA256SUMS.d/ /SHA256SUMS.d
RUN echo "${TARGETARCH:-amd64}" | sed -e s/amd64/x86_64/ -e s/arm64/aarch64/ | tee /target_uname_m
COPY --from=build-buildg /out/buildg /out/bin/
COPY --from=build-runc /out/runc.${TARGETARCH:-amd64} /out/bin/runc
RUN mkdir -p /out/share/doc/buildg-full && \
  echo "# buildg (full distribution)" > /out/share/doc/buildg-full/README.md && \
  echo "- buildg: $(cd /go/src/github.com/ktock/buildg && git describe --tags)" >> /out/share/doc/buildg-full/README.md && \
  echo "- runc: ${RUNC_VERSION}" >> /out/share/doc/buildg-full/README.md
ARG ROOTLESSKIT_VERSION
RUN fname="rootlesskit-$(cat /target_uname_m).tar.gz" && \
  curl -o "${fname}" -fSL "https://github.com/rootless-containers/rootlesskit/releases/download/${ROOTLESSKIT_VERSION}/${fname}" && \
  grep "${fname}" "/SHA256SUMS.d/rootlesskit-${ROOTLESSKIT_VERSION}" | sha256sum -c && \
  tar xzf "${fname}" -C /out/bin && \
  rm -f "${fname}" /out/bin/rootlesskit-docker-proxy && \
  echo "- RootlessKit: ${ROOTLESSKIT_VERSION}" >> /out/share/doc/buildg-full/README.md
ARG SLIRP4NETNS_VERSION
RUN fname="slirp4netns-$(cat /target_uname_m)" && \
  curl -o "${fname}" -fSL "https://github.com/rootless-containers/slirp4netns/releases/download/${SLIRP4NETNS_VERSION}/${fname}" && \
  grep "${fname}" "/SHA256SUMS.d/slirp4netns-${SLIRP4NETNS_VERSION}" | sha256sum -c && \
  mv "${fname}" /out/bin/slirp4netns && \
  chmod +x /out/bin/slirp4netns && \
  echo "- slirp4netns: ${SLIRP4NETNS_VERSION}" >> /out/share/doc/buildg-full/README.md
RUN echo "" >> /out/share/doc/buildg-full/README.md && \
  echo "## License" >> /out/share/doc/buildg-full/README.md && \
  echo "- bin/slirp4netns: [GNU GENERAL PUBLIC LICENSE, Version 2](https://github.com/rootless-containers/slirp4netns/blob/${SLIRP4NETNS_VERSION}/COPYING)" >> /out/share/doc/buildg-full/README.md && \
  echo "- Other files: [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0)" >> /out/share/doc/buildg-full/README.md && \
  (cd /out && find ! -type d | sort | xargs sha256sum > /tmp/SHA256SUMS ) && \
  mv /tmp/SHA256SUMS /out/share/doc/buildg-full/SHA256SUMS && \
  chown -R 0:0 /out

FROM scratch AS out-full
COPY --from=build-full /out /

# create buildg container
FROM ubuntu:22.04
ARG TARGETARCH
RUN apt-get update && apt-get install -y ca-certificates
COPY --from=out-full / /usr/local/
VOLUME /var/lib/buildg
ENTRYPOINT [ "/usr/local/bin/buildg"]
