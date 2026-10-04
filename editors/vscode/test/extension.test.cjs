const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

function loadExtension(startError) {
  const calls = { watchers: [] };
  class LanguageClient {
    constructor(id, name, server, options) {
      calls.server = server;
      calls.options = options;
    }
    async start() { calls.started = true; if (startError) throw startError; }
    async stop() { calls.stopped = true; }
  }
  const vscode = {
    workspace: {
      getConfiguration: () => ({ get: () => '/tools/bork' }),
      createFileSystemWatcher: pattern => {
        calls.watchers.push(pattern);
        return { dispose() {} };
      },
    },
    window: { showErrorMessage: async message => { calls.error = message; } },
  };
  const sandbox = {
    module: { exports: {} },
    require: name => name === 'vscode' ? vscode : { LanguageClient, TransportKind: { stdio: 0 } },
  };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../extension.cjs'), 'utf8'), sandbox);
  return { extension: sandbox.module.exports, calls };
}

test('client launches configured binary over stdio and stops on deactivation', async () => {
  const { extension, calls } = loadExtension();
  const context = { subscriptions: [] };
  await extension.activate(context);
  assert.equal(calls.server.command, '/tools/bork');
  assert.equal(calls.server.args.join(' '), 'lsp');
  assert.equal(calls.server.transport, 0);
  assert.equal(calls.options.documentSelector[0].language, 'bork');
  assert.equal(calls.watchers.includes('**/bork.mod'), true);
  assert.equal(context.subscriptions.length, 5);
  await extension.deactivate();
  assert.equal(calls.stopped, true);
});

test('missing compiler produces actionable startup error', async () => {
  const { extension, calls } = loadExtension(new Error('ENOENT'));
  await extension.activate({ subscriptions: [] });
  assert.match(calls.error, /bork.serverPath/);
  assert.match(calls.error, /ENOENT/);
});
