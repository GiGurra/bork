'use strict';
const childProcess = require('node:child_process');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');

function registerDebugging(vscode, context, spawn = childProcess.spawn) {
  const sessions = new Map();
  const prepared = new Map();
  const children = new Set();
  const kill = child => {
    if (!child?.pid || child.exitCode !== null) return;
    if (process.platform === 'win32') {
      const killer = spawn('taskkill', ['/PID', String(child.pid), '/T', '/F']);
      killer.on('error', () => child.kill());
    } else {
      try { process.kill(-child.pid, 'SIGTERM'); } catch { child.kill(); }
      const timeout = setTimeout(() => { try { process.kill(-child.pid, 'SIGKILL'); } catch {} }, 1000);
      timeout.unref();
      child.once('exit', () => clearTimeout(timeout));
    }
  };
  const release = async directory => {
    const entry = prepared.get(directory);
    prepared.delete(directory);
    entry?.cancel?.dispose();
    await fs.rm(directory, { recursive: true, force: true });
  };
  const output = vscode.window.createOutputChannel('bork Debug');
  const binary = () => vscode.workspace.getConfiguration('bork').get('serverPath', 'bork');
  const command = (args, cwd, token) => new Promise((resolve, reject) => {
    if (token?.isCancellationRequested) { reject(new Error('Debug startup canceled.')); return; }
    const process = spawn(binary(), args, { cwd, detached: globalThis.process.platform !== 'win32' });
    children.add(process);
    const cancellation = token?.onCancellationRequested(() => kill(process));
    const finish = () => { children.delete(process); cancellation?.dispose(); };
    let stderr = '';
    process.stdout.on('data', chunk => output.append(chunk.toString()));
    process.stderr.on('data', chunk => { stderr += chunk; output.append(chunk.toString()); });
    process.once('error', error => { finish(); reject(error); });
    process.once('exit', code => { finish(); code === 0 && !token?.isCancellationRequested ? resolve() : reject(new Error(stderr.trim() || `bork exited with code ${code}`)); });
  });
  const stop = async id => {
    const entry = sessions.get(id);
    if (!entry) return;
    sessions.delete(id);
    entry.cancellation.cancel();
    entry.cancellation.dispose();
    kill(entry.process);
    await release(entry.directory);
  };
  const provider = {
    resolveDebugConfiguration(folder, config) {
      if (!config.type) Object.assign(config, { type: 'bork', request: 'launch', name: 'Debug bork' });
      if (config.request !== 'launch') throw new Error('bork debugging currently supports launch requests.');
      config.program ||= folder?.uri.fsPath || vscode.window.activeTextEditor?.document.uri.fsPath;
      if (!config.program) throw new Error('Open a bork file or workspace before debugging.');
      return config;
    },
    async resolveDebugConfigurationWithSubstitutedVariables(folder, config, token) {
      if (!await vscode.workspace.saveAll(false)) throw new Error('Save bork files before debugging.');
      const cwd = config.cwd || folder?.uri.fsPath || path.dirname(config.program);
      config.program = path.resolve(cwd, config.program);
      config.cwd = cwd;
      const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'bork-debug-'));
      const entry = {};
      prepared.set(directory, entry);
      entry.cancel = token?.onCancellationRequested(() => { void release(directory); });
      try {
        const executable = path.join(directory, process.platform === 'win32' ? 'program.exe' : 'program');
        await command(['debug', 'build', config.program, '-o', executable], cwd, token);
        if (token?.isCancellationRequested) throw new Error('Debug startup canceled.');
        config.program = executable;
        config.mode = 'exec';
        config.__borkDirectory = directory;
        return config;
      } catch (error) {
        await release(directory);
        throw error;
      }
    },
  };
  const factory = {
    async createDebugAdapterDescriptor(session) {
      const config = session.configuration;
      const directory = config.__borkDirectory;
      if (!directory || !prepared.has(directory)) throw new Error('Missing or canceled bork debug build.');
      prepared.get(directory).cancel?.dispose();
      prepared.delete(directory);
      const cancellation = new vscode.CancellationTokenSource();
      sessions.set(session.id, { directory, cancellation });
      try {
        const delve = vscode.workspace.getConfiguration('bork').get('delvePath', '');
        const args = ['debug', 'dap', '--listen', '127.0.0.1:0'];
        if (delve) args.push('--delve', delve);
        const launch = () => new Promise((resolve, reject) => {
          if (!sessions.has(session.id) || cancellation.token.isCancellationRequested) { reject(new Error('Debug startup canceled.')); return; }
          const adapter = spawn(binary(), args, { cwd: config.cwd, detached: process.platform !== 'win32' });
          sessions.get(session.id).process = adapter;
          let text = '';
          let ready = false;
          const timeout = setTimeout(() => { kill(adapter); reject(new Error('Debugger startup timed out.')); }, 30000);
          const accept = chunk => {
            const value = chunk.toString();
            text += value;
            output.append(value);
            const match = text.match(/DAP server listening at: (127\.0\.0\.1|\[::1\]):(\d+)/);
            if (match && !ready) { ready = true; clearTimeout(timeout); resolve(new vscode.DebugAdapterServer(Number(match[2]), match[1])); }
          };
          adapter.stdout.on('data', accept);
          adapter.stderr.on('data', accept);
          adapter.once('error', error => { clearTimeout(timeout); reject(error); });
          adapter.once('exit', () => { clearTimeout(timeout); if (!ready) reject(new Error(text.trim() || 'Debugger exited before starting.')); });
        });
        try { return await launch(); }
        catch (error) {
          if (delve || !error.message.includes('debugger not installed')) throw error;
          const choice = await vscode.window.showErrorMessage('bork needs its optional debugger. Install the pinned debugger using Go?', 'Install debugger');
          if (choice !== 'Install debugger') throw error;
          await command(['debug', 'setup'], config.cwd, cancellation.token);
          return await launch();
        }
      } catch (error) {
        await stop(session.id);
        throw error;
      }
    },
  };
  context.subscriptions.push(output,
    vscode.debug.registerDebugConfigurationProvider('bork', provider),
    vscode.debug.registerDebugAdapterDescriptorFactory('bork', factory),
    vscode.debug.onDidTerminateDebugSession(session => { void stop(session.id); }),
    { dispose() { for (const child of children) kill(child); for (const directory of prepared.keys()) void release(directory); for (const id of sessions.keys()) void stop(id); } });
  return { provider, factory, stop };
}
module.exports = { registerDebugging };
