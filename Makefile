CMD_DESTDIR ?= /usr/local
PREFIX ?= $(CURDIR)/out/
CMD=buildg

PKG=github.com/ktock/buildg
VERSION=$(shell git describe --match 'v[0-9]*' --dirty='.m' --always --tags)
REVISION=$(shell git rev-parse HEAD)$(shell if ! git diff --no-ext-diff --quiet --exit-code; then echo .m; fi)
GO_EXTRA_LDFLAGS=-extldflags '-static'
GO_LD_FLAGS=-ldflags '-s -w -X $(PKG)/pkg/version.Version=$(VERSION) -X $(PKG)/pkg/version.Revision=$(REVISION) $(GO_EXTRA_LDFLAGS)'
GO_BUILDTAGS=-tags "osusergo netgo static_build"

all: build

build: buildg

buildg:
	CGO_ENABLED=0 go build -o $(PREFIX)/buildg $(GO_LD_FLAGS) $(GO_BUILDTAGS) -v .

install:
	install -D -m 755 $(PREFIX)/buildg $(CMD_DESTDIR)/bin
	install -D -m 755 $(CURDIR)/extras/buildg.sh $(CMD_DESTDIR)/bin
