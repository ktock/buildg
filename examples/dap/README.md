# Visual and Interactive Debugging of Dockerfile on editors via DAP (Debug Adapter Protocol)

Buildg allows visual and interactive debugging of Dockerfile on editors like VS Code, emacs and Neovim.

This is provided throgh [DAP(Debug Adapter Protocol)](https://microsoft.github.io/debug-adapter-protocol/) supported by editors [(official list)](https://microsoft.github.io/debug-adapter-protocol/implementors/tools/).

![Buildg on VS Code](../../docs/images/vscode-dap.png)

## Getting Started

- VS Code: https://github.com/ktock/vscode-buildg
- Emacs: [`./emacs`](./emacs)
- Neovim: [`./nvim`](./nvim)

## Launch Configuration

In the launch configuration (e.g. `launch.json` on VS Code), the following properties are provided.

- `program` *string* **REQUIRED** : Absolute path to Dockerfile.
- `stopOnEntry` *boolean* : Automatically stop after launch. (default: `true`)
- `target` *string* : Target build stage to build.
- `image` *string* : Image to use for debugging stage.
- `build-args` *array* : Build-time variables.
- `ssh` *array* : Allow forwarding SSH agent to the build. Format: `default|<id>[=<socket>|<key>[,<key>]]`
- `secrets` *array* : Expose secret value to the build. Format: `id=secretname,src=filepath`

