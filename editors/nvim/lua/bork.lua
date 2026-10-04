local M = {}

function M.setup(options)
  if vim.fn.has('nvim-0.11') == 0 then
    error('bork requires Neovim 0.11 or later')
  end
  vim.lsp.config('bork', vim.tbl_deep_extend('force', M.config(), options or {}))
  vim.lsp.enable('bork')
end

function M.config()
  return dofile(vim.api.nvim_get_runtime_file('lsp/bork.lua', false)[1])
end

return M
