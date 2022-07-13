[[⬇️ **Download]**](https://github.com/ktock/buildg/releases)
[[📖 **Command reference]**](#command-reference)

# buildg: Interactive debugger for Dockerfile

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

For the detailed command refenrece, refer to [Command reference](#command-reference) in the following

### Exmaple with terminal

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
WARN[2022-05-17T09:01:16Z] using host network as the default            
#1 [internal] load build definition from Dockerfile
#1 transferring dockerfile: 195B done
#1 DONE 0.1s

#2 [internal] load .dockerignore
#2 transferring context: 2B done
#2 DONE 0.1s

#3 [internal] load metadata for docker.io/library/busybox:latest
INFO[2022-05-17T09:01:19Z] debug session started. type "help" for command reference. 
Filename: "Dockerfile"
 =>   1| FROM busybox AS build1
      2| RUN echo hello > /hello
      3| 
 =>   4| FROM busybox AS build2
      5| RUN echo hi > /hi
      6| 
      7| FROM scratch
(buildg) break 5
(buildg) breakpoints
[0]: line: Dockerfile:5
[on-fail]: breaks on fail
(buildg) continue
#3 DONE 3.2s

#4 [build2 1/2] FROM docker.io/library/busybox@sha256:d2b53584f580310186df7a2055ce3ff83cc0df6caacf1e3489bff8cf5d0af5d8
#4 resolve docker.io/library/busybox@sha256:d2b53584f580310186df7a2055ce3ff83cc0df6caacf1e3489bff8cf5d0af5d8 0.0s done
#4 sha256:50e8d59317eb665383b2ef4d9434aeaa394dcd6f54b96bb7810fdde583e9c2d1 0B / 772.81kB 0.2s
#4 sha256:50e8d59317eb665383b2ef4d9434aeaa394dcd6f54b96bb7810fdde583e9c2d1 772.81kB / 772.81kB 0.9s done
Breakpoint[0]: reached line: Dockerfile:5
Filename: "Dockerfile"
      2| RUN echo hello > /hello
      3| 
      4| FROM busybox AS build2
*=>   5| RUN echo hi > /hi
      6| 
      7| FROM scratch
      8| COPY --from=build1 /hello /
(buildg) exec --image sh
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
bin  dev  etc  hi  home  proc  root  tmp  usr  var
# cat /debugroot/hi
hi
# 
(buildg) quit
```

## Use on IDEs

Buildg allows visual and interactive debugging of Dockerfile on editors like VS Code, emacs and Neovim.
This is provided throgh [DAP(Debug Adapter Protocol)](https://microsoft.github.io/debug-adapter-protocol/) supported by editors [(official list)](https://microsoft.github.io/debug-adapter-protocol/implementors/tools/).

See [`./examples/dap/README.md`](./examples/dap/README.md) for usage of DAP.

![Buildg on VS Code](./docs/images/vscode-dap.png)

## Install

- Requirements
  - [runc](https://github.com/opencontainers/runc)
  - [OPTIONAL] [RootlessKit](https://github.com/rootless-containers/rootlesskit) and [slirp4netns](https://github.com/rootless-containers/slirp4netns) for rootless execution

> NOTE: Native execution is supported only on Linux as of now. On other platforms, please run buildg on Linux VM (e.g. [Lima](https://github.com/lima-vm/lima), etc)

### Release binaries

Available from https://github.com/ktock/buildg/releases

### Rootless mode

Install and use [`buildg.sh`](./extras/buildg.sh).
[RootlessKit](https://github.com/rootless-containers/rootlesskit) and [slirp4netns](https://github.com/rootless-containers/slirp4netns) are needed.

```
$ buildg.sh debug /path/to/context
```

> NOTE: [buildg on IDEs (VS Code, Emacs, Neovim, etc.)](./examples/dap/) require this configuration

The doc in BuildKit project for troubleshooting: https://github.com/moby/buildkit/blob/master/docs/rootless.md#troubleshooting

### Building binary using make

Go 1.18+ is needed.

```
$ git clone https://github.com/ktock/buildg
$ cd buildg
$ make
$ sudo make install
```

### nerdctl

[nerdctl](https://github.com/containerd/nerdctl) project provides buildg as a subcommand since v0.20.0: https://github.com/containerd/nerdctl/blob/v0.20.0/docs/builder-debug.md

```
$ nerdctl builder debug /path/to/build/context
```

## Motivation

Debugging a large and complex Dockerfile isn't easy and can take a long time.
The goal of buildg is to solve it by providing a way to inspect the detailed execution state of a Dockerfile in an interactive and easy-to-use UI/UX.

BuildKit project has been working on better debugging support (e.g. https://github.com/moby/buildkit/pull/2813, https://github.com/moby/buildkit/issues/1472, https://github.com/moby/buildkit/issues/749).
Leveraging the generic features added through the work, this project implements a PoC for providing easy UI/UX to debug Dockerfile.

## Similar projects

- [`buildctl` by BuildKit](https://github.com/moby/buildkit) : has debug commands to inspect buildkitd, LLB, etc. but no interactive debugging for builds.
- [cntr](https://github.com/Mic92/cntr) : allows attaching and debugging containers but no interactive debugging for builds.
- [ctr by containerd](https://github.com/containerd/containerd) : allows directly controlling and inspecting containerd resources (e.g. contents, snapshots, etc.) but no interactive debugging for builds.

---

# Command reference

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [buildg debug](#buildg-debug)
- [buildg prune](#buildg-prune)
- [buildg du](#buildg-du)
- [buildg dap serve](#buildg-dap-serve)
- [buildg dap prune](#buildg-dap-prune)
- [buildg dap du](#buildg-dap-du)
- [Debug shell commands](#debug-shell-commands)
  - [break](#break)
  - [breakpoints](#breakpoints)
  - [clear](#clear)
  - [clearall](#clearall)
  - [next](#next)
  - [continue](#continue)
  - [exec](#exec)
  - [list](#list)
  - [exit](#exit)
  - [help](#help)
- [Global flags](#global-flags)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

## buildg debug

Debug a build. This starts building the specified Dockerfile and launches a debug session.

Usage: `buildg debug [OPTIONS] CONTEXT`

Flags:

- `--file value`, `-f value`: Name of the Dockerfile
- `--target value`: Target build stage to build.
- `--build-arg value`: Build-time variables
- `--image value`: Image to use for debugging stage. Specify `--image` flag for [`exec`](#exec) command in debug shell when use this image.
- `--secret value` : Secret value exposed to the build. Format: `id=secretname,src=filepath`
- `--ssh value` : Allow forwarding SSH agent to the build. Format: `default|<id>[=<socket>|<key>[,<key>]]`
- `--cache-from value`: Import build cache from the specified location. e.g. `user/app:cache`, `type=local,src=path/to/dir` (see [`./docs/cache-from.md`](./docs/cache-from.md))
- `--cache-reuse` : Reuse locally cached previous results (experimental).

## buildg prune

Prune cache

Usage: `buildg prune [OPTIONS]`

Flags:

- `--all`: Prune including internal/frontend references

## buildg du

Show disk usage information

Usage: `buildg du`

## buildg dap serve

Serve Debug Adapter Protocol (DAP) via stdio.
Should be called from editors. 
See [`./examples/dap/README.md`](./examples/dap/README.md) for usage of DAP.

Usage: `buildg dap serve [OPTIONS]`

Flags:
- `--log-file value`: Path to the file to output logs

## buildg dap prune

Prune DAP cache.
See [`./examples/dap/README.md`](./examples/dap/README.md) for usage of DAP.

Usage: `buildg dap prune [OPTIONS]`

Flags:

- `--all`: Prune including internal/frontend references

## buildg dap du

Show disk usage of DAP cache
See [`./examples/dap/README.md`](./examples/dap/README.md) for usage of DAP.

Usage: `buildg dap du`

## Debug shell commands

### break

Set a breakpoint.

Alias: `b`

Usage: `break BREAKPOINT`

The following value can be set as a `BREAKPOINT`.

- line number: breaks at the line number in Dockerfile
- `on-fail`: breaks on step that returns an error

### breakpoints

Show breakpoints key-value pairs.

Alias: `bp`

Usage: `breakpoints`

### clear

Clear a breakpoint. Specify breakpoint key.

Usage: `clear BREAKPOINT_KEY`

`BREAKPOINT_KEY` is the key of a breakpoint which is printed when executing `breakpoints` command.

### clearall

Clear all breakpoints.

Usage: `clearall`

### next

Proceed to the next line

Alias: `n`

Usage: `next`

### continue

Proceed to the next breakpoint

Alias: `c`

Usage: `continue`

### exec

Execute command in the step.
Only supported on RUN instructions as of now.

Alias: `e`

Usage: `exec [OPTIONS] [ARGS...]`

If `ARGS` isn't provided, `/bin/sh` is used by default.

Flags:

- `--image`: Execute command in the debuger image specified by `--image` flag of [`buildg debug`](#buildg-debug). If not specified, the command is executed on the rootfs of the current step.
- `--mountroot value`: Mountpoint to mount the rootfs of the step. ignored if `--image` isn't specified. (default: `/debugroot`)
- `--init-state`: Execute commands in an initial state of that step (experimental)
- `--tty`, `-t`: Allocate tty (enabled by default)
- `-i`: Enable stdin. (FIXME: must be set with tty) (enabled by default)
- `--env value`, `-e value`: Set environment variables
- `--workdir value`, `-w value`: Working directory inside the container

### list

List source lines

Aliases: `ls`, `l`

Usage: `list [OPTIONS]`

Flags:

- `--all`: show all lines
- `-A value`: Print the specified number of lines after the current line (default: 3)
- `-B value`: Print the specified number of lines before the current line (default: 3)
- `--range value`: Print the specified number of lines before and after the current line (default: 3)

### exit

Exit command

Aliases: `quit`, `q`

Usage: `exit`

### help

Shows a list of commands or help for one command

Alias: `h`

Usage: `help [COMMAND]`

## Global flags

- `--root` : Path to the root directory for storing data (e.g. "/var/lib/buildg").
- `--oci-worker-snapshotter value`: Worker snapshotter: "auto", "overlayfs", "native" (default: "auto")
- `--oci-worker-net value`: Worker network type: "auto", "cni", "host" (default: "auto")
- `--oci-cni-config-path value`: Path to CNI config file (default: "/etc/buildkit/cni.json")
- `--oci-cni-binary-path value`: Path to CNI plugin binary dir (default: "/opt/cni/bin")
- `--rootless`: Enable rootless configuration

# Additional documents

- [`./docs/cache-from.md`](./docs/cache-from.md): Inspecting remotely cached build using `--cache-from` flag.
