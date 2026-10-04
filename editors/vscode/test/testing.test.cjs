const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const { EventEmitter } = require('node:events');

function harness({ action = 'pass', output = '', cancel = false, malformed = false } = {}) {
  const calls = { args: [], outcomes: [], commands: new Map() };
  const uri = { fsPath: '/project/a.bork', scheme: 'file', toString: () => 'file:///project/a.bork' };
  const createToken = cancelled => {
    const listeners = new Set();
    return { isCancellationRequested: cancelled,
      onCancellationRequested: fn => { listeners.add(fn); return { dispose() { listeners.delete(fn); } }; },
      cancel() { this.isCancellationRequested = true; listeners.forEach(fn => fn()); },
    };
  };
  const token = createToken(cancel);
  calls.runToken = createToken(cancel);
  const collection = () => {
    const values = new Map();
    values.add = item => values.set(item.id, item);
    values.replace = items => { values.clear(); items.forEach(item => values.add(item)); };
    // TestItemCollection's forEach takes the item, unlike Map.
    values.forEach = fn => Map.prototype.forEach.call(values, item => fn(item));
    return values;
  };
  const disposable = { dispose() {} };
  const controller = {
    items: collection(), dispose() {},
    createTestItem: (id, label, uri) => ({ id, label, uri, children: collection() }),
    createRunProfile: (_, __, fn) => { calls.run = fn; },
    createTestRun: () => Object.assign({ token: calls.runToken }, Object.fromEntries(['passed', 'failed', 'errored', 'skipped', 'started', 'enqueued', 'end', 'appendOutput']
      .map(name => [name, (...args) => calls.outcomes.push([name, ...args])]))),
  };
  const watcher = { ...disposable, onDidCreate: () => disposable, onDidChange: () => disposable, onDidDelete: fn => { calls.delete = fn; return disposable; } };
  const vscode = {
    tests: { createTestController: () => controller },
    TestRunProfileKind: { Run: 1 }, Range: class { constructor(...args) { this.args = args; } },
    TestMessage: class { constructor(message) { this.message = message; } }, Location: class {},
    Uri: { parse: () => uri }, TestRunRequest: class { constructor(include) { this.include = include; } },
    CancellationTokenSource: class { constructor() { this.token = createToken(false); } cancel() { this.token.cancel(); } dispose() {} },
    workspace: { getConfiguration: () => ({ get: () => '/tools/bork' }),
      findFiles: async () => [uri], saveAll: async () => { if (calls.onSave) await calls.onSave(); return true; }, textDocuments: [],
      onDidOpenTextDocument: () => disposable, onDidSaveTextDocument: () => disposable,
      onDidChangeTextDocument: () => disposable, createFileSystemWatcher: () => watcher },
    commands: { registerCommand: (name, fn) => { calls.commands.set(name, fn); return disposable; } },
    window: { showErrorMessage: async message => { calls.error = message; },
      createTerminal: options => { calls.terminal = options; return { show() {}, dispose() {} }; } },
  };
  const spawn = (binary, args, options) => {
    calls.args.push({ binary, args, options });
    const child = new EventEmitter(); child.stdout = new EventEmitter(); child.stderr = new EventEmitter(); child.kill = () => { calls.killed = true; };
    process.nextTick(() => {
      if (calls.cancelDuringRun) calls.runToken.cancel();
      child.stderr.emit('data', output);
      child.stdout.emit('data', malformed ? 'noise' : JSON.stringify({ action, name: metadata[0].name, file: '/project/a.bork', line: 3, message: 'failure' }) + '\n');
      child.emit('close', action === 'fail' ? 1 : 0);
    });
    return child;
  };
  const sandbox = { module: { exports: {} }, require: name => name === 'vscode' ? vscode : name === 'node:child_process' ? { spawn } : require(name) };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../testing.cjs'), 'utf8'), sandbox);
  const metadata = [{ id: '/project/a.bork:3', name: 'first', uri: uri.toString(), path: '/project', location: '/project/a.bork:3', range: { start: { line: 2, character: 0 }, end: { line: 2, character: 4 } } }];
  const client = { sendRequest: async (method, params) => { calls.discovery = { method, params }; return metadata; } };
  sandbox.module.exports.registerTesting({ subscriptions: [] }, client);
  return { calls, controller, token, uri, metadata, execute: sandbox.module.exports.execute };
}

test('Test Explorer discovers through LSP and runs selected CLI tests with structured outcomes', async () => {
  for (const action of ['pass', 'fail', 'skip']) {
    const h = harness({ action, output: 'user output\n' });
    await h.controller.resolveHandler();
    assert.equal(h.calls.discovery.method, 'bork/tests');
    const file = h.controller.items.get(h.uri.toString());
    assert.equal(file.children.size, 1);
    await h.calls.run({ include: [file] }, h.token);
    assert.equal(h.calls.args[0].binary, '/tools/bork');
    assert.equal(h.calls.args[0].args.join('|'), 'test|--json|--filter|first|/project');
    assert.equal(h.calls.args[0].options.shell, false);
    assert.equal(h.calls.args[0].options.cwd, '/project');
    assert.ok(h.calls.outcomes.some(([name]) => name === { pass: 'passed', fail: 'failed', skip: 'skipped' }[action]));
    assert.ok(h.calls.outcomes.some(([name, text]) => name === 'appendOutput' && text === 'user output\r\n'));
    assert.equal(h.calls.outcomes.at(-1)[0], 'end');
  }
});

test('excluded tests, cancellation, invalid JSON and deleted files are handled', async () => {
  const h = harness(); await h.controller.resolveHandler();
  const file = h.controller.items.get(h.uri.toString());
  await h.calls.run({ include: [file], exclude: [file] }, h.token);
  assert.equal(h.calls.args.length, 0);
  h.calls.delete(h.uri); assert.equal(h.controller.items.size, 0);
  const cancelled = harness({ cancel: true }); await cancelled.controller.resolveHandler();
  await cancelled.calls.run({}, cancelled.token);
  assert.equal(cancelled.calls.args.length, 0);
  assert.ok(cancelled.calls.outcomes.some(([name]) => name === 'skipped'));
  const invalid = harness({ malformed: true }); await invalid.controller.resolveHandler();
  await invalid.calls.run({}, invalid.token);
  assert.ok(invalid.calls.outcomes.some(([name, , message]) => name === 'errored' && /Invalid bork test JSON/.test(message.message)));
});

test('run lens passes CLI arguments without a command shell', async () => {
  const h = harness();
  await h.calls.commands.get('bork.run')(h.uri.toString(), 'script', '/project/a.bork');
  assert.equal(h.calls.terminal.shellPath, '/tools/bork');
  assert.equal(h.calls.terminal.shellArgs.join('|'), 'script|/project/a.bork');
});


test('file selections await saved rediscovery and preserve TestItem identity', async () => {
  const h = harness(); await h.controller.resolveHandler();
  const file = h.controller.items.get(h.uri.toString());
  const original = file.children.get(h.metadata[0].id);
  await h.controller.resolveHandler(file);
  assert.equal(h.controller.items.get(h.uri.toString()), file);
  assert.equal(file.children.get(original.id), original);
  h.calls.onSave = async () => {
    h.metadata[0] = { ...h.metadata[0], id: 'new-id', name: 'new test' };
  };
  await h.calls.run({ include: [file] }, h.token);
  assert.equal(h.calls.args[0].args[3], 'new test');
  assert.ok(h.calls.outcomes.some(([name]) => name === 'passed'));
});

test('TestRun UI cancellation kills the executing CLI and skips the test', async () => {
  const h = harness(); await h.controller.resolveHandler();
  h.calls.cancelDuringRun = true;
  await h.calls.run({}, h.token);
  assert.equal(h.calls.killed, true);
  assert.ok(h.calls.outcomes.some(([name]) => name === 'skipped'));
  assert.equal(h.calls.outcomes.at(-1)[0], 'end');
});
