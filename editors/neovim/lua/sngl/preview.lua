local M = {}

function M.setup(opts)
  vim.api.nvim_create_user_command("SnglPreview", function()
    local clients = vim.lsp.get_clients({ name = "sngl" })
    if #clients == 0 then
      vim.notify("sngl LSP client not attached", vim.log.levels.ERROR)
      return
    end
    local client = clients[1]
    local uri = vim.uri_from_bufnr(0)
    local pos = vim.api.nvim_win_get_cursor(0)
    local params = {
      command = "sngl.openPreview",
      arguments = {
        { uri = uri, position = { line = pos[1] - 1, character = pos[2] } },
      },
    }
    client.request("workspace/executeCommand", params, function(err, result)
      if err then
        vim.notify("sngl preview: " .. tostring(err.message or err), vim.log.levels.ERROR)
        return
      end
      if not result or not result.url then
        vim.notify("sngl preview: no URL in response", vim.log.levels.ERROR)
        return
      end
      vim.ui.open(result.url)
    end, 0)
  end, { desc = "Open SNGL side-panel preview in browser" })
end

return M
