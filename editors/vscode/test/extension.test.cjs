const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

function loadExtension(startError) {
  const calls = { watchers: [], clients: [], commands: {}, serverPath: '/tools/bork' };
  const State = { Stopped: 1, Starting: 3, Running: 2 };
  class LanguageClient {
    constructor(id, name, server, options) {
      calls.server = server;
      calls.options = options;
      calls.clients.push(this);
      this.initializeResult = { serverInfo: { version: 'v0.4.2' } };
    }
    onDidChangeState(listener) {
      this.changeState = newState => listener({ newState });
      return { dispose() { calls.listenerDisposed = true; } };
    }
    async start() {
      calls.started = true;
      this.running = false;
      this.changeState(State.Starting);
      if (startError) { this.failed = true; throw startError; }
      this.running = true;
      this.changeState(State.Running);
    }
    isRunning() { return this.running; }
    async dispose() {
      calls.stopped = true; this.disposed = true;
      if (this.failed) throw new Error("Client is not running and cannot be stopped");
    }
  }
  const status = { show() { calls.visible = true; }, hide() { calls.visible = false; }, dispose() {} };
  calls.status = status;
  const vscode = {
    StatusBarAlignment: { Left: 1 },
    commands: { registerCommand(name, fn) { calls.commands[name] = fn; return { dispose() {} }; } },
    workspace: {
      getConfiguration: () => ({ get: () => calls.serverPath }),
      createFileSystemWatcher: pattern => {
        calls.watchers.push(pattern);
        return { dispose() {} };
      },
    },
    window: {
      activeTextEditor: { document: { languageId: 'bork' } },
      createStatusBarItem: () => status,
      createOutputChannel: () => ({ show() { calls.outputShown = true; }, appendLine(line) { calls.outputLine = line; }, dispose() {} }),
      onDidChangeActiveTextEditor(fn) { calls.editorChanged = fn; return { dispose() {} }; },
      showQuickPick: async () => calls.pick,
      showErrorMessage: async message => { calls.error = message; },
    },
  };
  const sandbox = {
    module: { exports: {} },
    require: name => name === './testing.cjs' ? { registerTesting() { calls.testing = true; } } : name === 'vscode' ? vscode : { LanguageClient, State, TransportKind: { stdio: 0 } },
  };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../extension.cjs'), 'utf8'), sandbox);
  return { extension: sandbox.module.exports, calls, vscode, State, clearStartError() { startError = undefined; } };
}

test('client launches configured binary over stdio and disposes on deactivation', async () => {
  const { extension, calls } = loadExtension();
  await extension.activate({ subscriptions: [] });
  assert.equal(calls.testing, true);
  assert.equal(calls.server.command, '/tools/bork');
  assert.equal(calls.server.args.join(' '), 'lsp');
  assert.equal(calls.server.transport, 0);
  assert.equal(calls.options.documentSelector[0].language, 'bork');
  assert.equal(calls.watchers.includes('**/bork.mod'), true);
  assert.equal(calls.watchers.includes('**/bork.sum'), true);
  assert.equal(calls.watchers.includes('**/go-deps.mod'), true);
  assert.equal(calls.watchers.includes('**/go-deps.sum'), true);
  await extension.deactivate();
  assert.equal(calls.stopped, true);
  assert.equal(calls.listenerDisposed, true);
});

test('missing compiler produces actionable startup error and failed status', async () => {
  const { extension, calls } = loadExtension(new Error('ENOENT'));
  await extension.activate({ subscriptions: [] });
  assert.match(calls.error, /bork.serverPath/);
  assert.match(calls.error, /ENOENT/);
  assert.match(calls.status.text, /Failed/);
  assert.match(calls.outputLine, /ENOENT/);
  await extension.deactivate();
});

test('status follows server state and active language; click offers output and restart', async () => {
  const { extension, calls, vscode, State } = loadExtension();
  await extension.activate({ subscriptions: [] });
  assert.equal(calls.status.text, 'bork v0.4.2: Running');
  assert.equal(calls.status.command, 'bork.serverStatus');
  assert.equal(calls.visible, true);
  calls.clients[0].changeState(State.Stopped);
  assert.match(calls.status.text, /Stopped/);
  vscode.window.activeTextEditor = undefined;
  calls.editorChanged();
  assert.equal(calls.visible, false);
  calls.pick = 'Show output';
  await calls.commands['bork.serverStatus']();
  assert.equal(calls.outputShown, true);
  calls.serverPath = '/new/bork';
  calls.pick = 'Restart language server';
  await calls.commands['bork.serverStatus']();
  assert.equal(calls.clients[0].disposed, true);
  assert.equal(calls.server.command, '/new/bork');
  assert.equal(calls.clients.length, 2);
  assert.equal(calls.status.text, 'bork v0.4.2: Running');
  await extension.deactivate();
});

test('concurrent restart commands replace the server once', async () => {
  const { extension, calls } = loadExtension();
  await extension.activate({ subscriptions: [] });
  await Promise.all([calls.commands['bork.restartServer'](), calls.commands['bork.restartServer']()]);
  assert.equal(calls.clients.length, 2);
  await extension.deactivate();
});

test('language defaults use formatter indentation and balanced bracket rules', () => {
  const manifest = require('../package.json');
  const defaults = manifest.contributes.configurationDefaults['[bork]'];
  assert.equal(defaults['editor.formatOnSave'], true);
  assert.equal(defaults['editor.defaultFormatter'], 'gigurra.bork');
  assert.equal(defaults['editor.tabSize'], 2);
  assert.equal(defaults['editor.insertSpaces'], true);
  const config = require('../language-configuration.json');
  const increase = new RegExp(config.indentationRules.increaseIndentPattern);
  const decrease = new RegExp(config.indentationRules.decreaseIndentPattern);
  for (const line of ['fn main() {', 'values = [', 'call(', 'call(other(),', 'fn main() { // call()']) assert.equal(increase.test(line), true, line);
  for (const line of ['// comment {', 'fn main() {}', 'call()', 'message = "{"']) assert.equal(increase.test(line), false, line);
  for (const line of ['  }', ']', ')']) assert.equal(decrease.test(line), true, line);
});

test('restart recovers immediately after a failed connection attempt', async () => {
  const { extension, calls, clearStartError } = loadExtension(new Error('ENOENT'));
  await extension.activate({ subscriptions: [] });
  clearStartError();
  calls.serverPath = '/fixed/bork';
  await calls.commands['bork.restartServer']();
  assert.equal(calls.clients.length, 2);
  assert.equal(calls.clients[0].disposed, true);
  assert.equal(calls.server.command, '/fixed/bork');
  assert.equal(calls.status.text, 'bork v0.4.2: Running');
  await extension.deactivate();
});
