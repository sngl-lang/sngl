local M = {}

function M.setup(opts)
  local ok, parsers = pcall(require, "nvim-treesitter.parsers")
  if not ok then
    return
  end

  local parser_configs = parsers.get_parser_configs and parsers.get_parser_configs() or parsers
  -- Resolve path: this file is at editors/neovim/lua/sngl/treesitter.lua
  -- tree-sitter-sngl is at editors/tree-sitter-sngl/
  local this_dir = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":h")
  local ts_dir = vim.fn.fnamemodify(this_dir .. "/../../../tree-sitter-sngl", ":p")

  parser_configs.sngl = {
    install_info = {
      url = ts_dir,
      files = { "src/parser.c", "src/scanner.c" },
      generate_requires_npm = false,
      requires_generate_from_grammar = false,
    },
    filetype = "sngl",
  }
end

return M
