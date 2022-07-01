# Visual and Interactive Debugging of Dockerfile on editors via DAP (Debug Adapter Protocol)

Buildg allows visual and interactive debugging of Dockerfile on editors like VS Code, emacs and Neovim.

This is provided throgh [DAP(Debug Adapter Protocol)](https://microsoft.github.io/debug-adapter-protocol/) supported by editors [(official list)](https://microsoft.github.io/debug-adapter-protocol/implementors/tools/).

![Buildg on VS Code](../../docs/images/vscode-dap.png)

## Getting Started

- VS Code: https://github.com/ktock/vscode-buildg
- Emacs: [`./emacs`](./emacs)
- Neovim: [`./nvim`](./nvim)

## Repl commands

### exec

Execute command in the step.
Only supported on RUN instructions as of now.

Alias: `e`

Usage: `exec [OPTIONS] [ARGS...]`

If `ARGS` isn't provided, `/bin/sh` is used by default.

Flags:

- `--image`: Execute command in the debuger image specified by `image` property in the lauch configuration. If not specified, the command is executed on the rootfs of the current step.
- `--mountroot value`: Mountpoint to mount the rootfs of the step. ignored if `--image` isn't specified. (default: `/debugroot`)
- `--init-state`: Execute commands in an initial state of that step (experimental)
- `--tty`, `-t`: Allocate tty (enabled by default)
- `-i`: Enable stdin. (FIXME: must be set with tty) (enabled by default)
- `--env value`, `-e value`: Set environment variables
- `--workdir value`, `-w value`: Working directory inside the container

### help

Shows a list of commands or help for one command

Alias: `h`

Usage: `help [COMMAND]`

## Launch Configuration

In the launch configuration (e.g. `launch.json` on VS Code), the following properties are provided.

- `program` *string* **REQUIRED** : Absolute path to Dockerfile.
- `stopOnEntry` *boolean* : Automatically stop after launch. (default: `true`)
- `target` *string* : Target build stage to build.
- `image` *string* : Image to use for debugging stage.
- `build-args` *array* : Build-time variables.
- `ssh` *array* : Allow forwarding SSH agent to the build. Format: `default|<id>[=<socket>|<key>[,<key>]]`
- `secrets` *array* : Expose secret value to the build. Format: `id=secretname,src=filepath`

