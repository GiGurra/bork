'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const manifest = require('../package.json');
const root = path.resolve(__dirname, '..');

assert.match(manifest.publisher, /^[a-z0-9][a-z0-9-]*$/, 'publisher must match both registry namespaces');
assert.match(manifest.version, /^\d+\.\d+\.\d+$/, 'use a stable semantic extension version');
assert.ok(manifest.categories.includes('Programming Languages'), 'missing language category');
assert.ok(manifest.keywords.includes('bork') && manifest.keywords.includes('lsp'), 'missing discovery keywords');
assert.equal(manifest.license, 'MIT');
assert.ok(manifest.homepage && manifest.bugs.url && manifest.repository.directory);
for (const file of ['README.md', 'CHANGELOG.md', 'LICENSE', manifest.main, manifest.icon]) {
  assert.ok(fs.statSync(path.join(root, file)).isFile(), `missing packaged file: ${file}`);
}
assert.ok(fs.readFileSync(path.join(root, 'CHANGELOG.md'), 'utf8').includes(`## ${manifest.version}`),
  'add the extension version to CHANGELOG.md before releasing');
const icon = fs.readFileSync(path.join(root, manifest.icon));
assert.equal(icon.subarray(0, 8).toString('hex'), '89504e470d0a1a0a', 'icon must be a PNG');
assert.ok(icon.readUInt32BE(16) >= 128 && icon.readUInt32BE(20) >= 128, 'icon must be at least 128×128');
console.log(`Validated ${manifest.publisher}.${manifest.name}@${manifest.version}`);
