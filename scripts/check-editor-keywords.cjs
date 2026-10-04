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
];
for (const grammar of grammars) {
  const source = fs.readFileSync(path.join(root, grammar), 'utf8');
  for (const keyword of keywords) {
    assert.ok(new RegExp(`\\b${keyword}\\b`).test(source), `${grammar} omits compiler keyword ${keyword}`);
  }
}
console.log(`All ${keywords.length} compiler keywords occur in ${grammars.length} editor grammars.`);
