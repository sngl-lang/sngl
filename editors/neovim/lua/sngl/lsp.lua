local M = {}

-- The server's own token type, and standard ones whose default group suits
-- SNGL poorly. Default links, so a colour scheme that sets them wins.
local links = {
  ["@lsp.type.component.sngl"] = "@tag",
  ["@lsp.type.event.sngl"] = "@function",
  ["@lsp.typemod.variable.readonly.sngl"] = "@constant",
}

local function link()
  for group, target in pairs(links) do
    vim.api.nvim_set_hl(0, group, { link = target, default = true })
  end
end

function M.setup(opts)
  link()
  vim.api.nvim_create_autocmd("ColorScheme", { callback = link })

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
