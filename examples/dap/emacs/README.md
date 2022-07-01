# Buildg on emacs

Buildg supports interactive debugging of Dockerfile on emacs.

![Buildg on Emacs](../../../docs/images/emacs-dap.png)

## Install

- Requirements
  - buildg
  - [dap-mode](https://github.com/emacs-lsp/dap-mode)
    - configuration guide: https://emacs-lsp.github.io/dap-mode/page/configuration/

Then add the example launch configuration shown in [`./dap-dockerfile.el`](./dap-dockerfile.el) to your `init.el`.
Also refer to [`../README.md`](../README.md) for available properties in the launch configuration.

## Usage

Run `M-x dap-debug` then select `Dockerfile Debug Configuration` template.
