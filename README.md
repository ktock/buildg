# buildg: A tool to debug Dockerfile based on BuildKit

`buildg` is a tool to interactively debug Dockerfile based on [BuildKit](https://github.com/moby/buildkit).

- Source-level inspection
- Breakpoints and step execution
- Interactive shell on a step with your own debugigng tools
- Based on BuildKit (needs unmerged patches)
- Supports rootless

**early stage software** This is implemented based on BuildKit with some unmerged patches. We're planning to upstream them.

## How to use

```
buildg debug /path/to/build/context
```

To use your own image for debugging steps:

```
buildg debug --image=debugging-tools /path/to/build/context
```

### Exmaple

Debug the following Dockerfile:

```Dockerfile
FROM busybox AS build1
RUN echo hello > /hello

FROM busybox AS build2
RUN echo hi > /hi

FROM scratch
COPY --from=build1 /hello /
COPY --from=build2 /hi /
```

Store this Dockerfile to somewhere (e.g. `/tmp/ctx/Dockerfile`) then run `buildg debug`.
`buildg.sh` can be used for rootless execution (discussed later).

```console
$ buildg.sh debug --image=ubuntu:22.04 /tmp/ctx
WARN[2022-05-09T01:40:21Z] using host network as the default            
#1 [internal] load .dockerignore
#1 transferring context: 2B done
#1 DONE 0.1s

#2 [internal] load build definition from Dockerfile
#2 transferring dockerfile: 195B done
#2 DONE 0.1s

#3 [internal] load metadata for docker.io/library/busybox:latest
#3 DONE 3.0s

#4 [build1 1/2] FROM docker.io/library/busybox@sha256:d2b53584f580310186df7a2055ce3ff83cc0df6caacf1e3489bff8cf5d0af5d8
#4 resolve docker.io/library/busybox@sha256:d2b53584f580310186df7a2055ce3ff83cc0df6caacf1e3489bff8cf5d0af5d8 0.0s done
#4 sha256:50e8d59317eb665383b2ef4d9434aeaa394dcd6f54b96bb7810fdde583e9c2d1 772.81kB / 772.81kB 0.2s done
Filename: "Dockerfile"
      2| RUN echo hello > /hello
      3| 
      4| FROM busybox AS build2
 =>   5| RUN echo hi > /hi
      6| 
      7| FROM scratch
      8| COPY --from=build1 /hello /
>>> break 2
>>> breakpoints
[0]: line 2
>>> continue
#4 extracting sha256:50e8d59317eb665383b2ef4d9434aeaa394dcd6f54b96bb7810fdde583e9c2d1 0.0s done
#4 DONE 0.3s

#5 [build2 2/2] RUN echo hi > /hi
#5 ...

#6 [build1 2/2] RUN echo hello > /hello
Filename: "Dockerfile"
      1| FROM busybox AS build1
*=>   2| RUN echo hello > /hello
      3| 
      4| FROM busybox AS build2
      5| RUN echo hi > /hi
>>> exec --image sh
# cat /etc/os-release
PRETTY_NAME="Ubuntu 22.04 LTS"
NAME="Ubuntu"
VERSION_ID="22.04"
VERSION="22.04 LTS (Jammy Jellyfish)"
VERSION_CODENAME=jammy
ID=ubuntu
ID_LIKE=debian
HOME_URL="https://www.ubuntu.com/"
SUPPORT_URL="https://help.ubuntu.com/"
BUG_REPORT_URL="https://bugs.launchpad.net/ubuntu/"
PRIVACY_POLICY_URL="https://www.ubuntu.com/legal/terms-and-policies/privacy-policy"
UBUNTU_CODENAME=jammy
# ls /debugroot/
bin  dev  etc  hello  home  proc  root	tmp  usr  var
# cat /debugroot/hello
hello
# 
>>> quit
```

## Install

- Requirements
  - [runc](https://github.com/opencontainers/runc)
  - [OPTIONAL] [RootlessKit](https://github.com/rootless-containers/rootlesskit) and [slirp4netns](https://github.com/rootless-containers/slirp4netns) for rootless execution

### Release binaries

Available from https://github.com/ktock/buildg/releases

### Building using make

Go 1.18+ is needed.

```
$ git clone https://github.com/ktock/buildg
$ cd buildg
$ make
$ sudo make install
```

### Rootless mode

Install and use [`buildg.sh`](./extras/buildg.sh).
[RootlessKit](https://github.com/rootless-containers/rootlesskit) and [slirp4netns](https://github.com/rootless-containers/slirp4netns) are needed.

```
$ buildg.sh debug /tmp/mybuild
```

The doc in BuildKit project for troubleshooting: https://github.com/moby/buildkit/blob/master/docs/rootless.md#troubleshooting

## Motivation

Debugging a large and complex Dockerfile isn't easy and can take a long time.
The goal of buildg is to solve it by providing a way to insepct the detailed execution state of a Dockerfile in an interactive and easy-to-use UI/UX.

BuildKit project has been working on better debugging support (e.g. https://github.com/moby/buildkit/pull/2813, https://github.com/moby/buildkit/issues/1472, https://github.com/moby/buildkit/issues/749).
Leveraging the generic features added through the work, this project implements a PoC for providing easy UI/UX to debug Dockerfile.

## Similar projects

- [`buildctl` by BuildKit](https://github.com/moby/buildkit) : has debug commands to inspect buildkitd, LLB, etc. but no interactive debugging for builds.
- [cntr](https://github.com/Mic92/cntr) : allows attaching and debugging containers but no interactive debugging for builds.
- [ctr by containerd](https://github.com/containerd/containerd) : allows directly controlling and inspecting containerd resources (e.g. contents, snapshots, etc.) but no interactive debugging for builds.

## Command reference

```console
$ buildg --help
NAME:
   buildg - A debug tool for Dockerfile based on BuildKit

USAGE:
   buildg [global options] command [command options] [arguments...]

COMMANDS:
   debug    Debug a build
   version  Version info
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --debug     enable debug logs
   --help, -h  show help
```

### buildg debug

```console
$ buildg debug --help
NAME:
   buildg debug - Debug a build

USAGE:
   buildg debug [command options] [arguments...]

OPTIONS:
   --file value, -f value       Name of the Dockerfile
   --target value               Target build stage to build.
   --build-arg value            Build-time variables
   --oci-worker-net value       Worker network type: "auto", "cni", "host" (default: "auto")
   --image value                Image to use for debugging stage
   --oci-cni-config-path value  Path to CNI config file (default: "/etc/buildkit/cni.json")
   --oci-cni-binary-path value  Path to CNI plugin binary dir (default: "/opt/cni/bin")
   --rootless                   Enable rootless configuration
```

#### Debug commands

```
COMMANDS:

break, b LINE_NUMBER      set a breakpoint
breakpoints, bp           list breakpoints
clear LINE_NUMBER         clear a breakpoint
clearall                  clear all breakpoints
next, n                   proceed to the next line
continue, c               proceed to the next breakpoint
exec, e [OPTIONS] ARG...  execute command in the step
  --image          use debugger image
  --mountroot=DIR  mountpoint to mount the rootfs of the step. ignored if --image isn't specified.
  --init           execute commands in an initial state of that step (experimental)
list, ls, l [OPTIONS]     list lines
  --all  list all lines in the source file
exit, quit, q             exit the debugging
```
