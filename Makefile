CMD_DESTDIR ?= /usr/local
PREFIX ?= $(CURDIR)/out/
CMD=buildg

all: build

build: buildg

buildg:
	go build -o $(PREFIX)/buildg -v .

install:
	install -D -m 755 $(PREFIX)/buildg $(CMD_DESTDIR)/bin
	install -D -m 755 $(CURDIR)/extras/buildg.sh $(CMD_DESTDIR)/bin
