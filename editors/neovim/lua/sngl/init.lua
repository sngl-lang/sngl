local M = {}

function M.setup(opts)
  opts = opts or {}
  require("sngl.treesitter").setup(opts)
  require("sngl.lsp").setup(opts)
end

return M
