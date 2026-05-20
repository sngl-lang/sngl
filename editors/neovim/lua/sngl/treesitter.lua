local M = {}

-- setup wires the bundled sngl tree-sitter parser into nvim's runtime so
-- syntax highlighting works without depending on nvim-treesitter's
-- known-languages list (the main branch only ships built-in parsers).
--
-- The parser .so lives in the sibling tree-sitter-sngl/ checkout next to
-- this plugin. Queries are discovered via runtimepath at queries/sngl/.
function M.setup(opts)
  -- Resolve absolute path to the bundled parser. This file is at
  -- editors/neovim/lua/sngl/treesitter.lua; the .so is at
  -- editors/tree-sitter-sngl/sngl.so (three dirs up, then over).
  local this_dir = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":h")
  local ts_dir = vim.fn.fnamemodify(this_dir .. "/../../../tree-sitter-sngl", ":p")
  local so_path = ts_dir .. "sngl.so"

  if vim.fn.filereadable(so_path) == 0 then
    -- Parser not built; surface a hint rather than failing silently.
    vim.notify(
      "sngl: tree-sitter parser not found at " .. so_path ..
      "\nBuild it with: cd " .. ts_dir .. " && tree-sitter build",
      vim.log.levels.WARN
    )
    return
  end

  -- Nvim 0.10+: language.add(name, {path = ...}) registers an external
  -- parser. Falls through to nvim-treesitter config for older nvims.
  local lang_add = vim.treesitter.language and vim.treesitter.language.add
  if lang_add then
    pcall(lang_add, "sngl", { path = so_path })
  end

  -- Map filetype -> tree-sitter language. Required even when the
  -- filetype name matches the language name on some nvim versions.
  if vim.treesitter.language and vim.treesitter.language.register then
    pcall(vim.treesitter.language.register, "sngl", "sngl")
  end

  -- Make the bundled queries dir discoverable. The plugin already ships
  -- queries at editors/neovim/queries/sngl/*.scm; ensure that dir is on
  -- the runtime path. Most plugin managers handle this automatically,
  -- but explicit prepend covers manual setups.
  local plugin_dir = vim.fn.fnamemodify(this_dir .. "/../..", ":p")
  vim.opt.runtimepath:prepend(plugin_dir)

  -- Auto-attach the tree-sitter highlighter when an sngl buffer loads.
  vim.api.nvim_create_autocmd("FileType", {
    pattern = "sngl",
    callback = function(args)
      pcall(vim.treesitter.start, args.buf, "sngl")
    end,
  })

  -- Legacy nvim-treesitter config (only used on the old master branch).
  -- The main branch ignores parser_configs but accepting it doesn't
  -- harm.
  local ok, parsers = pcall(require, "nvim-treesitter.parsers")
  if ok then
    local parser_configs = parsers.get_parser_configs and parsers.get_parser_configs() or parsers
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
end

return M
