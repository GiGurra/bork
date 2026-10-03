const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { test, before } = require('node:test');
const tm = require('vscode-textmate');
const onig = require('vscode-oniguruma');
let grammar;

// Test embedding with a small source.go stand-in. BORK_GO_GRAMMAR can point to
// the real Go grammar for an integration run without vendoring another grammar.
const goFixture = { scopeName: 'source.go', patterns: [
  { name: 'comment.line.double-slash.go', begin: '//', end: '$' },
  { name: 'comment.block.go', begin: '/\\*', end: '\\*/' },
  { name: 'string.quoted.double.go', begin: '"', end: '"', patterns: [{ match: '\\\\.', name: 'constant.character.escape.go' }] },
  { name: 'string.quoted.single.go', begin: "'", end: "'", patterns: [{ match: '\\\\.', name: 'constant.character.escape.go' }] },
  { name: 'string.quoted.raw.go', begin: '`', end: '`' },
  { name: 'keyword.control.go', match: '\\b(if|return|var)\\b' },
] };

before(async () => {
  const wasm = fs.readFileSync(require.resolve('vscode-oniguruma/release/onig.wasm'));
  await onig.loadWASM(wasm.buffer.slice(wasm.byteOffset, wasm.byteOffset + wasm.byteLength));
  const registry = new tm.Registry({
    onigLib: Promise.resolve({ createOnigScanner: p => new onig.OnigScanner(p), createOnigString: s => new onig.OnigString(s) }),
    loadGrammar: async scope => {
      if (scope === 'source.bork') {
        const file = path.join(__dirname, '../syntaxes/bork.tmLanguage.json');
        return tm.parseRawGrammar(fs.readFileSync(file, 'utf8'), file);
      }
      if (scope === 'source.go') {
        if (process.env.BORK_GO_GRAMMAR) return tm.parseRawGrammar(fs.readFileSync(process.env.BORK_GO_GRAMMAR, 'utf8'), process.env.BORK_GO_GRAMMAR);
        return goFixture;
      }
      return null;
    },
  });
  grammar = await registry.loadGrammar('source.bork');
});
function tokenize(src) {
  let stack = tm.INITIAL;
  return src.split('\n').map(line => {
    const result = grammar.tokenizeLine(line, stack);
    stack = result.ruleStack;
    return { line, tokens: result.tokens };
  });
}
function scopes(lines, row, text) {
  const col = lines[row].line.indexOf(text);
  assert.ok(col >= 0, `missing ${text}`);
  return lines[row].tokens.find(t => t.startIndex <= col && col < t.endIndex).scopes;
}
function has(lines, row, text, scope) {
  assert.ok(scopes(lines, row, text).includes(scope), `${text}: expected ${scope}, got ${scopes(lines, row, text)}`);
}

test('declarations, contextual keywords, maps, numbers, rune and pipeline', () => {
  const ls = tokenize('fn f[T](x: T): T { x }\npred positive(x: Int) { x > 0 }\ntype T = sealed { Empty } derive (Encode)\nrule r(x: Int) { positive(x) => positive(x) }\nscope s with taskTimeout(1) { trust positive(1); mock Fetch(url) { url } }\n{"n": [0xff, 0b10, 0o7, 1_000, 1.5e-3], "r": \'\\n\'} |> show');
  has(ls, 0, 'fn', 'storage.type.function.bork');
  has(ls, 0, 'f[', 'entity.name.function.bork');
  has(ls, 1, 'positive', 'entity.name.function.bork');
  has(ls, 2, 'T', 'entity.name.type.bork');
  for (const word of ['sealed', 'derive']) has(ls, 2, word, 'keyword.control.bork');
  has(ls, 3, 'rule', 'storage.type.function.bork');
  for (const word of ['scope', 'with', 'trust', 'mock']) has(ls, 4, word, 'keyword.control.bork');
  for (const number of ['0xff', '0b10', '0o7', '1_000', '1.5e-3']) has(ls, 5, number, 'constant.numeric.bork');
  has(ls, 5, '|>', 'keyword.operator.pipeline.bork');
  has(ls, 5, '\\n', 'constant.character.escape.bork');
});

test('interpolation nests expressions and ignores literal dollars', () => {
  const ls = tokenize('s"Hi $name $$ ${f({"x": "}"}) + 1}!"\n"plain $name ${1}"');
  has(ls, 0, '$name', 'variable.other.interpolated.bork');
  has(ls, 0, '$$', 'constant.character.escape.bork');
  has(ls, 0, 'f(', 'meta.embedded.expression.bork');
  has(ls, 0, '"x"', 'string.quoted.double.bork');
  has(ls, 0, '1', 'constant.numeric.bork');
  assert.ok(!scopes(ls, 0, '!').includes('meta.interpolation.bork'));
  assert.ok(!scopes(ls, 1, '$name').includes('variable.other.interpolated.bork'));
});

test('comments mask delimiters and keywords', () => {
  const ls = tokenize('// fn }\n/* type {\nmatch */ fn f() {}');
  has(ls, 0, 'fn', 'comment.line.double-slash.bork');
  has(ls, 1, 'type', 'comment.block.bork');
  has(ls, 2, 'match', 'comment.block.bork');
  has(ls, 2, 'fn', 'storage.type.function.bork');
});

test('unsafe Go survives nested braces, strings, runes and comments', () => {
  const ls = tokenize('fn f(): Int unsafe go {\nvar text = `}`\n/* } */ if true {\n  var other = "}"; var rune = \'}\' // }\n}\nreturn 1\n}\nfn next(): Int { 2 }');
  has(ls, 0, 'unsafe', 'keyword.control.unsafe.bork');
  for (const [row, text] of [[1, 'var'], [2, 'if'], [3, 'var'], [5, 'return']]) has(ls, row, text, 'meta.embedded.block.go');
  for (const [row, text] of [[1, 'var'], [5, 'return']]) {
    assert.ok(scopes(ls, row, text).some(scope => scope.startsWith('keyword.') && scope.endsWith('.go')));
  }
  has(ls, 7, 'fn', 'storage.type.function.bork');
  assert.ok(!scopes(ls, 7, 'next').includes('meta.embedded.block.go'));
});

test('every example and case source tokenizes without exhausting the time limit', () => {
  const root = path.resolve(__dirname, '../../..');
  function walk(dir) {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const file = path.join(dir, entry.name);
      if (entry.isDirectory()) walk(file);
      else if (entry.name.endsWith('.bork')) {
        let stack = tm.INITIAL;
        for (const line of fs.readFileSync(file, 'utf8').split('\n')) {
          const result = grammar.tokenizeLine(line, stack, 1000);
          assert.equal(result.stoppedEarly, false, file);
          stack = result.ruleStack;
        }
      }
    }
  }
  walk(path.join(root, 'examples'));
  walk(path.join(root, 'testdata/cases'));
});


test('unfinished single-line literals recover before the next declaration', () => {
  for (const literal of ['"unfinished', "'x", 's"unfinished $name', 's"${1']) {
    const ls = tokenize(`fn f(): String { ${literal}\n}\nfn next(): Int { 1 }`);
    has(ls, 2, 'fn', 'storage.type.function.bork');
    has(ls, 2, 'next', 'entity.name.function.bork');
    assert.ok(!scopes(ls, 2, 'next').some(scope => scope.startsWith('string.')));
  }
});

test('effect signatures and function types', () => {
  const ls = tokenize('fn send(x: Int) uses io + net: Int { x }\nfn pure() uses nothing {}\ntype Callback = (Int) uses io => Int');
  for (const row of [0, 1, 2]) has(ls, row, 'uses', 'keyword.control.bork');
  has(ls, 0, '+', 'keyword.operator.bork');
  has(ls, 2, '=>', 'keyword.operator.bork');
});

test('private record construction is highlighted as a contextual keyword', () => {
  const ls = tokenize('type Config = private { port: Int }');
  has(ls, 0, 'Config', 'entity.name.type.bork');
  has(ls, 0, 'private', 'keyword.control.bork');
});

test('sequence producers and iteration control', () => {
  const ls = tokenize('generate[Int] { for (n in Seq.range(0, 3)) { yield n; continue; break } }');
  for (const word of ['generate', 'for', 'yield', 'continue', 'break']) {
    has(ls, 0, word, 'keyword.control.bork');
  }
});

test('specialized constructor heads retain type and variant scopes', () => {
  const ls = tokenize('x = Box[Int] { values: [] }\ny = Option[String].Some { value: "trace" }\nz = api.State[List[Int]].Empty');
  for (const [row, word] of [[0, 'Box'], [0, 'Int'], [1, 'Option'], [1, 'String'], [1, 'Some'], [2, 'State'], [2, 'List'], [2, 'Int'], [2, 'Empty']]) {
    has(ls, row, word, 'entity.name.type.bork');
  }
});

test('lazy is contextual at binding heads', () => {
 const ls = tokenize('lazy value: Int = compute()\nlazy other = value\nfn lazy(x: Int): Int { x }\nprintln(lazy(1))');
 has(ls, 0, 'lazy', 'keyword.control.bork');
 has(ls, 1, 'lazy', 'keyword.control.bork');
 assert.ok(!scopes(ls, 3, 'lazy').includes('keyword.control.bork'));
});

test('async is contextual at binding heads', () => {
 const ls = tokenize('async(s) value: Int = compute()\nasync(owner(s)) other = value\nfn async(x: Int): Int { x }\nprintln(async(1))');
 has(ls, 0, 'async', 'keyword.control.bork');
 has(ls, 1, 'async', 'keyword.control.bork');
 assert.ok(!scopes(ls, 2, 'async').includes('keyword.control.bork'));
 assert.ok(!scopes(ls, 3, 'async').includes('keyword.control.bork'));
});

test('lazy is contextual at independent field heads', () => {
 const ls = tokenize('type Lazy[T] = { lazy value: T }\ntype Named = { lazy: Int }');
 has(ls, 0, 'lazy', 'keyword.control.bork');
 assert.ok(!scopes(ls, 1, 'lazy').includes('keyword.control.bork'));
});
