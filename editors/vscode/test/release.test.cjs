'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');
const { releaseVersion } = require('../scripts/release.cjs');

test('release tags match the extension version, independently of compiler tags', () => {
  assert.equal(releaseVersion('tag', 'vscode-v0.1.0', '0.1.0'), '0.1.0');
  for (const [kind, name] of [['branch', 'main'], ['tag', 'v0.1.0'], ['tag', 'vscode-v0.2.0'], ['branch', 'vscode-v0.1.0']]) {
    assert.throws(() => releaseVersion(kind, name, '0.1.0'), /matching package.json/);
  }
});
