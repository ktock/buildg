#!/bin/sh

set -eu

# Executes buildg with removing the symlink to /run/runc existing in the parent namespace
# to allow runc creating their own file. The acutal file in the parent namespace are not removed.
RUN="rm -f /run/runc; exec buildg $@"

rootlesskit --net=slirp4netns --copy-up=/etc --copy-up=/run --disable-host-loopback /bin/sh -c "$RUN"
