# Buildg on Neovim

Buildg supports interactive debugging of Dockerfile on Neovim.

This depends on [nvim-dap](https://github.com/mfussenegger/nvim-dap/blob/master/doc/dap.txt).

![Buildg on Neovim](../../../docs/images/nvim-dap.png)

## Install

- Requirements
  - Neovim (>= 0.6)
  - buildg

- Via [packer.nvim](https://github.com/wbthomason/packer.nvim)
  - Add the example launch configuration shown in [`./plugins.lua`](./plugins.lua) to your packer configuration (e.g. `~/.config/nvim/init.lua`, `~/.config/nvim/lua/plugins.lua`, etc.).
    - Also refer to [`../README.md`](../README.md) for available properties in the launch configuration.

## Usage

```
nvim /path/to/Dockerfile
```

See also [`:help dap.txt`](https://github.com/mfussenegger/nvim-dap/blob/master/doc/dap.txt) of nvim-dap for available commands.
