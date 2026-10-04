local root = vim.fn.getcwd()
vim.opt.runtimepath:prepend(root .. '/editors/nvim')
vim.opt.runtimepath:prepend(assert(vim.env.BORK_TEST_SITE))
vim.cmd('filetype plugin indent on')
dofile(root .. '/editors/nvim/plugin/bork.lua')
require('bork').setup({ cmd = { assert(vim.env.BORK_TEST_BINARY), 'lsp' } })

-- Compile every adapted query against the shared parser, then highlight the
-- entire example/case corpus through Neovim's actual query engine.
local highlight_queries = {}
for _, editor in ipairs({ 'nvim', 'helix', 'zed' }) do
  local query_dir = editor == 'zed' and '/languages/bork' or '/queries/bork'
  for _, file in ipairs(vim.fn.glob(root .. '/editors/' .. editor .. query_dir .. '/*.scm', false, true)) do
    local query = vim.treesitter.query.parse('bork', table.concat(vim.fn.readfile(file), '\n'))
    if file:match('/highlights%.scm$') then highlight_queries[editor] = query end
  end
end
local literal_source = "fn main() { x = 1; y = true; z = 'x' }"
local literal_tree = vim.treesitter.get_string_parser(literal_source, 'bork'):parse()[1]
for editor, expected in pairs({
  nvim = { 'number', 'boolean', 'character' },
  helix = { 'constant.numeric', 'constant.builtin.boolean', 'constant.character' },
  zed = { 'number', 'boolean', 'string' },
}) do
  local captures = {}
  local query = highlight_queries[editor]
  for id in query:iter_captures(literal_tree:root(), literal_source) do captures[query.captures[id]] = true end
  for _, name in ipairs(expected) do assert(captures[name], editor .. ' omits literal capture ' .. name) end
end
for _, directory_name in ipairs({ 'examples', 'testdata/cases' }) do
  for _, file in ipairs(vim.fn.glob(root .. '/' .. directory_name .. '/**/*.bork', false, true)) do
    local source = table.concat(vim.fn.readfile(file), '\n')
    local parser = vim.treesitter.get_string_parser(source, 'bork')
    for _, query in pairs(highlight_queries) do
      for _ in query:iter_captures(parser:parse()[1]:root(), source) do end
    end
  end
end

if vim.env.BORK_TEST_NVIM_TREESITTER then
  vim.opt.runtimepath:append(vim.env.BORK_TEST_NVIM_TREESITTER)
  local buffer = vim.api.nvim_create_buf(false, true)
  vim.api.nvim_set_current_buf(buffer)
  vim.bo.filetype = 'bork'
  vim.bo.shiftwidth = 2
  vim.bo.expandtab = true
  vim.bo.indentexpr = "v:lua.require'nvim-treesitter'.indentexpr()"
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, {
    'fn main() {', 'if true {', 'println(1)', '}', '}',
  })
  vim.cmd('normal! gg=G')
  assert(vim.deep_equal(vim.api.nvim_buf_get_lines(buffer, 0, -1, false), {
    'fn main() {', '  if true {', '    println(1)', '  }', '}',
  }), 'tree-sitter indentation did not align closing braces')
  vim.api.nvim_buf_delete(buffer, { force = true })
end

local directory = vim.fn.tempname()
vim.fn.mkdir(directory, 'p')
vim.fn.writefile({ 'module example.com/editor-test' }, directory .. '/bork.mod')
local function attach(file, lines)
  vim.fn.writefile(lines, file)
  vim.cmd.edit(vim.fn.fnameescape(file))
  local buffer = vim.api.nvim_get_current_buf()
  assert(vim.bo[buffer].filetype == 'bork', 'filetype detection failed: ' .. file)
  assert(vim.wait(15000, function()
    local clients = vim.lsp.get_clients({ bufnr = buffer, name = 'bork' })
    return #clients == 1 and clients[1].initialized
  end, 50), 'bork LSP did not attach')
  assert(vim.wait(15000, function() return #vim.diagnostic.get(buffer) > 0 end, 50), 'compiler diagnostic did not arrive')
  local client = vim.lsp.get_clients({ bufnr = buffer, name = 'bork' })[1]
  local response = client:request_sync('textDocument/formatting', {
    textDocument = { uri = vim.uri_from_bufnr(buffer) },
    options = { tabSize = 2, insertSpaces = true },
  }, 10000, buffer)
  assert(response and not response.err and #response.result > 0, 'compiler formatting did not return edits: ' .. vim.inspect(response))
  local parser = vim.treesitter.get_parser(buffer, 'bork')
  assert(not parser:parse()[1]:root():has_error(), 'tree-sitter parser rejected the source')
  assert(vim.treesitter.query.get('bork', 'highlights'), 'highlight query did not load')
  return buffer
end
attach(directory .. '/main.bork', { 'fn main(){println(missing)}' })
attach(directory .. '/script', { '#!/usr/bin/env -S bork script', 'println( missing )' })
for _, client in ipairs(vim.lsp.get_clients({ name = 'bork' })) do client:stop(true) end
vim.fn.delete(directory, 'rf')
print('Neovim: filetype, script detection, LSP attach, compiler diagnostics/formatting and tree-sitter passed')
vim.cmd('qa!')
