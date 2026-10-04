vim.filetype.add({
  extension = { bork = 'bork' },
  pattern = { ['.*'] = { function(_, bufnr)
    local first = vim.api.nvim_buf_get_lines(bufnr, 0, 1, false)[1] or ''
    if first:match('^#!.*%f[%w]bork%f[%W]') then return 'bork' end
  end, { priority = -math.huge } } },
})
vim.api.nvim_create_autocmd('FileType', {
  pattern = 'bork',
  group = vim.api.nvim_create_augroup('bork_highlight', { clear = true }),
  callback = function(event)
    pcall(vim.treesitter.start, event.buf, 'bork')
  end,
})
