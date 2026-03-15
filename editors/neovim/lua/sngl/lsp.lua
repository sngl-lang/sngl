local M = {}

function M.setup(opts)
  vim.api.nvim_create_autocmd("FileType", {
    pattern = "sngl",
    callback = function(ev)
      vim.lsp.start({
        name = "sngl",
        cmd = { "sngl", "lsp" },
        root_dir = vim.fs.root(ev.buf, { "sngl.kdl", ".git" }),
        filetypes = { "sngl" },
      })
    end,
  })
end

return M
