ARG GO_VERSION=1.18
ARG RUNC_VERSION=v1.1.3

# syntax = docker/dockerfile:1.4.0
FROM --platform=${BUILDPLATFORM} golang:${GO_VERSION}-alpine AS base
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.* .

FROM --platform=$BUILDPLATFORM tonistiigi/xx:1.1.1@sha256:23ca08d120366b31d1d7fad29283181f063b0b43879e1f93c045ca5b548868e9 AS xx

FROM base AS build

COPY --from=xx / /

ARG TARGETPLATFORM
# https://pkg.go.dev/cmd/go#hdr-Build_and_test_caching
RUN --mount=type=bind,target=. \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
  xx-go build -o /out/buildg .

FROM --platform=${BUILDPLATFORM} golang:${GO_VERSION}-bullseye AS build-base-debian
RUN dpkg --add-architecture arm64 && \
  dpkg --add-architecture amd64 && \
  apt-get update && \
  apt-get install -y crossbuild-essential-amd64 crossbuild-essential-arm64 git libbtrfs-dev:amd64 libbtrfs-dev:arm64 libseccomp-dev:amd64 libseccomp-dev:arm64

FROM build-base-debian AS build-runc
ARG {RUNC_VERSION}
RUN git clone https://github.com/opencontainers/runc.git /go/src/github.com/opencontainers/runc
WORKDIR /go/src/github.com/opencontainers/runc
RUN git checkout ${RUNC_VERSION} && \
  mkdir -p /out
ENV CGO_ENABLED=1
RUN GOARCH=amd64 CC=x86_64-linux-gnu-gcc make static && \
  cp -a runc /out/runc.amd64
RUN GOARCH=arm64 CC=aarch64-linux-gnu-gcc make static && \
  cp -a runc /out/runc.arm64

FROM ubuntu:20.04 AS bin-unix

RUN <<EOF
  apt-get update
  apt-get install -y ca-certificates
EOF

COPY --from=build /out/buildg /
COPY --from=build-runc /out/runc.${TARGETARCH:-amd64} /usr/local/bin/runc

ENTRYPOINT [ "/buildg"]
