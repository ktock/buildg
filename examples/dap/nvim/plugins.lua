require'packer'.startup(function()
        use "mfussenegger/nvim-dap"
end)  

local dap = require("dap")

dap.adapters.dockerfile = {
  type = 'executable';
  command = 'buildg';
  args = { 'dap', "serve" };
}

dap.configurations.dockerfile = {
    {
        type = "dockerfile",
        name = "Dockerfile Configuration",
        request = "launch",
        stopOnEntry = true,
        program = "${file}",
    },
}
