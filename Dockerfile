# syntax = docker/dockerfile:1.4.0

ARG GO_VERSION=1.19
ARG RUNC_VERSION=v1.1.3

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

# create buildg container
FROM ubuntu:22.04
ARG TARGETARCH
RUN apt-get update && apt-get install -y ca-certificates git
COPY --from=build-buildg /out/buildg /
COPY --from=build-runc /out/runc.${TARGETARCH:-amd64} /usr/local/bin/runc
VOLUME /var/lib/buildg
ENTRYPOINT [ "/buildg"]
