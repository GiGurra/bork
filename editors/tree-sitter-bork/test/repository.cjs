const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const root = path.resolve(__dirname, '../../..');
const grammar = path.resolve(__dirname, '..');
const cli = path.join(grammar, 'node_modules/.bin/tree-sitter');
const recovery = new Set([
  'copy_separator_fail', // Assignment in a named update.
  'go_bindings_parse_fail', // A numeric unsafe Go body.
  'interpolation_errors_fail', // Invalid interpolation holes.
  'named_argument_separator_fail', // Assignment in a named argument.
  'number_errors_fail', // Invalid numeric tokens.
  'rune_and_constant_errors_fail', // A multi-character rune.
  'single_ampersand_fail', // An unsupported operator.
  'syntax_errors_fail', // Missing delimiters.
].map(name => `testdata/cases/${name}/main.bork`));
function files(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap(entry => {
    const file = path.join(dir, entry.name);
    return entry.isDirectory() ? files(file) : file.endsWith('.bork') ? [file] : [];
  });
}
const sources = ['examples', 'testdata/cases'].flatMap(dir => files(path.join(root, dir)));
const seen = new Set();
for (const file of sources) {
  const relative = path.relative(root, file).split(path.sep).join('/');
  const result = spawnSync(cli, ['parse', '--quiet', file], { cwd: grammar, encoding: 'utf8' });
  assert.ifError(result.error);
  const broken = /\((ERROR|MISSING)\b/.test(result.stdout);
  assert.ok(result.status === 0 || broken, `Parser failed for ${relative}: ${result.stderr}`);
  assert.equal(broken, recovery.has(relative), `${relative}: ${result.stdout || 'Recovery fixture unexpectedly parses cleanly'}`);
  seen.add(relative);
}
for (const file of recovery) assert.ok(seen.has(file), `Missing recovery fixture: ${file}`);
// Compile every query against the generated node schema, including queries which
// do not happen to capture anything in this small validation input.
for (const query of fs.readdirSync(path.join(grammar, 'queries'))) {
  const result = spawnSync(cli, ['query', path.join('queries', query), path.join(root, 'examples/hello/main.bork')], { cwd: grammar, encoding: 'utf8' });
  assert.equal(result.status, 0, `${query}: ${result.stderr}`);
}
const fixture = path.join(grammar, 'test/fixtures/queries.bork');
const fixtureParse = spawnSync(cli, ['parse', '--quiet', fixture], { cwd: grammar, encoding: 'utf8' });
assert.equal(fixtureParse.status, 0, fixtureParse.stdout + fixtureParse.stderr);
function captures(query) {
  const result = spawnSync(cli, ['query', path.join('queries', query), fixture], { cwd: grammar, encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
  return result.stdout;
}
const highlights = captures('highlights.scm');
for (const [scope, text] of [['keyword', 'match'], ['type.definition', 'User'], ['function', 'describe'], ['variable.parameter', 'user']]) {
  assert.ok(highlights.split('\n').some(line => line.includes(' - ' + scope + ',') && line.includes('text: `' + text + '`')), `Missing ${scope} capture for ${text}`);
}
const injection = captures('injections.scm').match(/injection\.content, start: \((\d+), (\d+)\), end: \((\d+), (\d+)\)/);
assert.ok(injection, 'Go content must be captured for injection');
const fixtureText = fs.readFileSync(fixture, 'utf8');
const lines = fixtureText.split('\n');
const offset = (row, column) => lines.slice(0, Number(row)).reduce((total, line) => total + line.length + 1, 0) + Number(column);
const body = fixtureText.slice(offset(injection[1], injection[2]), offset(injection[3], injection[4]));
assert.equal(body, fixtureText.match(/unsafe go \{([\s\S]*?)\n\}/)[1] + '\n', 'Go injection must include the full balanced body');
const locals = captures('locals.scm');
for (const name of ['result', 'self', 'app', 'value', 'item', 'message', 'first', 'rest', 'name']) {
  assert.ok(locals.split('\n').some(line => line.includes(' - local.definition,') && line.includes('text: `' + name + '`')), `Missing lexical definition for ${name}`);
}
assert.ok(!locals.split('\n').some(line => line.includes(' - local.definition,') && line.includes('text: `caseName`')), 'Qualified variant names must not define locals');
console.log(`Parsed ${sources.length} repository files (${recovery.size} explicit recovery fixtures); all queries compile.`);
