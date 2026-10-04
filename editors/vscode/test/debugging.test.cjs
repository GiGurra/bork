'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const { EventEmitter } = require('node:events');
const { registerDebugging } = require('../debugging.cjs');

function fixture({ missing = false, holdBuild = false, holdSetup = false } = {}) {
  const calls = [];
  const context = { subscriptions: [] };
  let installChoice = 'Install debugger';
  const spawn = (binary, args, options) => {
    const child = new EventEmitter();
    child.stdout = new EventEmitter(); child.stderr = new EventEmitter();
    child.exitCode = null; child.pid = 2000000000;
    child.kill = () => { child.killed = true; child.exitCode = 1; child.emit('exit', 1); };
    calls.push({ binary, args, options, child });
    const complete = () => {
      if (args[1] === 'dap') {
        if (missing) { child.stderr.emit('data', 'debugger not installed; run `bork debug setup`'); child.exitCode = 1; child.emit('exit', 1); }
        else child.stdout.emit('data', 'DAP server listening at: 127.0.0.1:4567\n');
      } else { if (args[1] === 'setup') missing = false; child.exitCode = 0; child.emit('exit', 0); }
    };
    if ((holdBuild && args[1] === 'build') || (holdSetup && args[1] === 'setup')) child.complete = complete;
    else setImmediate(complete);
    return child;
  };
  const vscode = {
    workspace: { getConfiguration: () => ({ get: (key, fallback) => key === 'serverPath' ? '/bork tool' : fallback }), saveAll: async () => true },
    window: { createOutputChannel: () => ({ append() {}, dispose() {} }), showErrorMessage: async () => installChoice },
    debug: { registerDebugConfigurationProvider: () => ({ dispose() {} }), registerDebugAdapterDescriptorFactory: () => ({ dispose() {} }), onDidTerminateDebugSession: () => ({ dispose() {} }) },
    CancellationTokenSource: class { constructor() { this.token = token(); } cancel() { this.token.cancel(); } dispose() {} },
    DebugAdapterServer: class { constructor(port, host) { this.port = port; this.host = host; } },
  };
  const api = registerDebugging(vscode, context, spawn);
  return { ...api, calls, context, decline() { installChoice = undefined; } };
}
function token() {
  const callbacks = new Set();
  return { isCancellationRequested: false, onCancellationRequested(fn) { callbacks.add(fn); return { dispose() { callbacks.delete(fn); } }; }, cancel() { this.isCancellationRequested = true; for (const fn of [...callbacks]) fn(); } };
}
const folder = { uri: { fsPath: '/project path' } };

test('debug preparation returns an executable launch; factory uses CLI-owned TCP DAP', async () => {
  const f = fixture(); const config = f.provider.resolveDebugConfiguration(folder, {});
  const prepared = await f.provider.resolveDebugConfigurationWithSubstitutedVariables(folder, config, token());
  assert.equal(prepared.mode, 'exec'); assert.notEqual(prepared.program, folder.uri.fsPath);
  assert.deepEqual(f.calls[0].args.slice(0, 3), ['debug', 'build', '/project path']);
  const adapter = await f.factory.createDebugAdapterDescriptor({ id: 'one', configuration: prepared });
  assert.equal(adapter.port, 4567); assert.equal(adapter.host, '127.0.0.1');
  assert.deepEqual(f.calls[1].args, ['debug', 'dap', '--listen', '127.0.0.1:0']);
  await f.stop('one'); await assert.rejects(fs.stat(prepared.__borkDirectory));
});

test('missing debugger offers pinned CLI installation and retries startup', async () => {
  const f = fixture({ missing: true });
  const config = await f.provider.resolveDebugConfigurationWithSubstitutedVariables(folder, f.provider.resolveDebugConfiguration(folder, {}), token());
  const adapter = await f.factory.createDebugAdapterDescriptor({ id: 'two', configuration: config });
  assert.equal(adapter.port, 4567); assert.deepEqual(f.calls[2].args, ['debug', 'setup']);
  await f.stop('two');
});

test('canceling a completed preparation cleans its directory before factory ownership', async () => {
  const f = fixture(); const cancellation = token();
  const config = await f.provider.resolveDebugConfigurationWithSubstitutedVariables(folder, f.provider.resolveDebugConfiguration(folder, {}), cancellation);
  cancellation.cancel();
  await assert.rejects(f.factory.createDebugAdapterDescriptor({ id: 'three', configuration: config }), /canceled/);
  await new Promise(resolve => setTimeout(resolve, 20));
  await assert.rejects(fs.stat(config.__borkDirectory));
});

test('canceling compilation stops the child and cleans preparation', async () => {
  const f = fixture({ holdBuild: true }); const cancellation = token();
  const preparing = f.provider.resolveDebugConfigurationWithSubstitutedVariables(folder, f.provider.resolveDebugConfiguration(folder, {}), cancellation);
  while (!f.calls.length) await new Promise(resolve => setImmediate(resolve));
  cancellation.cancel();
  assert.equal(f.calls[0].child.killed, true);
  await assert.rejects(preparing);
  const directory = require('node:path').dirname(f.calls[0].args.at(-1));
  await assert.rejects(fs.stat(directory));
});


test('stopping during installation cancels setup without launching another adapter', async () => {
  const f = fixture({ missing: true, holdSetup: true });
  const config = await f.provider.resolveDebugConfigurationWithSubstitutedVariables(folder, f.provider.resolveDebugConfiguration(folder, {}), token());
  const starting = f.factory.createDebugAdapterDescriptor({ id: 'four', configuration: config });
  while (!f.calls.some(call => call.args[1] === 'setup')) await new Promise(resolve => setImmediate(resolve));
  const rejected = assert.rejects(starting);
  await f.stop('four');
  await rejected;
  assert.equal(f.calls.at(-1).child.killed, true);
  assert.equal(f.calls.filter(call => call.args[1] === 'dap').length, 1);
  await assert.rejects(fs.stat(config.__borkDirectory));
});
