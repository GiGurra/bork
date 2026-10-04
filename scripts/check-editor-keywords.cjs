#!/usr/bin/env node
// Keyword additions must be reflected in every independently maintained grammar.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname, '..');
const tokens = fs.readFileSync(path.join(root, 'internal/syntax/token.go'), 'utf8');
const keywordMap = tokens.match(/var keywords = map\[string\]Kind\{([\s\S]*?)\n\}/)[1];
const keywords = [...keywordMap.matchAll(/"([a-z]+)"\s*:/g)].map(match => match[1]);
const grammars = [
  'editors/vscode/syntaxes/bork.tmLanguage.json',
  'editors/tree-sitter-bork/grammar.js',
  'editors/tree-sitter-bork/queries/highlights.scm',
  'editors/vim/syntax/bork.vim',
  'editors/emacs/bork-mode.el',
  'editors/nvim/queries/bork/highlights.scm',
  'editors/helix/queries/bork/highlights.scm',
  'editors/zed/languages/bork/highlights.scm',
];
for (const grammar of grammars) {
  const source = fs.readFileSync(path.join(root, grammar), 'utf8');
  for (const keyword of keywords) {
    assert.ok(new RegExp(`\\b${keyword}\\b`).test(source), `${grammar} omits compiler keyword ${keyword}`);
  }
}
console.log(`All ${keywords.length} compiler keywords occur in ${grammars.length} editor syntax sources.`);
