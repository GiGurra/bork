const vscode = require('vscode');
const path = require('node:path');
const { spawn } = require('node:child_process');
const process = require('node:process');

// The server discovers declarations; the CLI owns compilation and execution.
function registerTesting(context, client) {
  const controller = vscode.tests.createTestController('bork', 'bork');
  const metadata = new Map();
  context.subscriptions.push(controller);
  const binary = () => vscode.workspace.getConfiguration('bork').get('serverPath', 'bork');
  const discover = async uri => {
    const tests = await client.sendRequest('bork/tests', { textDocument: { uri: uri.toString() } });
    const id = uri.toString();
    const previous = controller.items.get(id);
    if (previous) previous.children.forEach(item => metadata.delete(item.id));
    if (!tests.length) { controller.items.delete(id); return; }
    const file = controller.createTestItem(id, path.basename(uri.fsPath), uri);
    for (const test of tests) {
      const item = controller.createTestItem(test.id, test.name, uri);
      item.range = new vscode.Range(test.range.start.line, test.range.start.character,
        test.range.end.line, test.range.end.character);
      metadata.set(item.id, test);
      file.children.add(item);
    }
    controller.items.add(file);
  };
  const discoverAll = async () => {
    const uris = await vscode.workspace.findFiles('**/*.bork', '**/{node_modules,.git}/**');
    for (const uri of uris) await discover(uri);
  };
  controller.resolveHandler = async item => item ? discover(item.uri) : discoverAll();
  controller.refreshHandler = discoverAll;
  const refresh = document => {
    if (document.languageId === 'bork' && document.uri.scheme === 'file') {
      discover(document.uri).catch(error => vscode.window.showErrorMessage(`bork test discovery: ${error.message}`));
    }
  };
  context.subscriptions.push(vscode.workspace.onDidOpenTextDocument(refresh),
    vscode.workspace.onDidSaveTextDocument(refresh), vscode.workspace.onDidChangeTextDocument(event => refresh(event.document)));
  const watcher = vscode.workspace.createFileSystemWatcher('**/*.bork');
  context.subscriptions.push(watcher, watcher.onDidCreate(uri => discover(uri).catch(() => {})),
    watcher.onDidChange(uri => discover(uri).catch(() => {})), watcher.onDidDelete(uri => {
      const item = controller.items.get(uri.toString());
      if (item) item.children.forEach(child => metadata.delete(child.id));
      controller.items.delete(uri.toString());
    }));

  const runHandler = async (request, token) => {
    const run = controller.createTestRun(request);
    try {
      if (!await vscode.workspace.saveAll(false)) throw new Error('Save bork files before running tests.');
      if (!request.include) await discoverAll();
      const excluded = new Set((request.exclude || []).map(item => item.id));
      const queue = [];
      const visit = item => {
        if (excluded.has(item.id)) return;
        if (metadata.has(item.id)) queue.push({ item, test: metadata.get(item.id) });
        else item.children.forEach(visit);
      };
      (request.include || Array.from(controller.items, ([, item]) => item)).forEach(visit);
      queue.forEach(({ item }) => run.enqueued(item));
      for (const { item, test } of queue) {
        if (token.isCancellationRequested) { run.skipped(item); continue; }
        run.started(item);
        try {
          const result = await execute(binary(), ['test', '--json', '--filter', test.name, test.path], token,
            text => run.appendOutput(text.replace(/\r?\n/g, '\r\n'), undefined, item));
          if (token.isCancellationRequested) { run.skipped(item); continue; }
          const event = result.events.find(event => event.name === test.name);
          if (!event) throw new Error(`bork exited ${result.code} without a result for ${test.name}.`);
          if (event.action === 'pass') run.passed(item);
          else if (event.action === 'skip') run.skipped(item);
          else {
            const message = new vscode.TestMessage(event.message || 'Test failed');
            message.location = new vscode.Location(item.uri, item.range);
            run.failed(item, message);
          }
        } catch (error) { run.errored(item, new vscode.TestMessage(error.message)); }
      }
    } catch (error) {
      run.appendOutput(`${error.message}\r\n`);
      await vscode.window.showErrorMessage(error.message);
    } finally { run.end(); }
  };
  controller.createRunProfile('Run', vscode.TestRunProfileKind.Run, runHandler, true);
  context.subscriptions.push(vscode.commands.registerCommand('bork.runTest', async test => {
    if (!test) return;
    await discover(vscode.Uri.parse(test.uri));
    const file = controller.items.get(test.uri);
    const item = file && file.children.get(test.id);
    if (item) {
      const cancellation = new vscode.CancellationTokenSource();
      try { await runHandler(new vscode.TestRunRequest([item]), cancellation.token); }
      finally { cancellation.dispose(); }
    }
  }));
  context.subscriptions.push(vscode.commands.registerCommand('bork.run', async (uri, mode, target) => {
    if (!uri || !['run', 'script'].includes(mode)) return;
    if (!await vscode.workspace.saveAll(false)) return;
    const terminal = vscode.window.createTerminal({ name: 'bork', shellPath: binary(), shellArgs: [mode, target],
      cwd: path.dirname(vscode.Uri.parse(uri).fsPath) });
    context.subscriptions.push(terminal);
    terminal.show();
  }));
  vscode.workspace.textDocuments.forEach(refresh);
}

function execute(binary, args, token, output) {
  return new Promise((resolve, reject) => {
    const child = spawn(binary, args, { shell: false, detached: process.platform !== 'win32' });
    let stdout = '';
    child.stdout.on('data', data => { stdout += data.toString(); });
    child.stderr.on('data', data => output(data.toString()));
    const kill = () => {
      if (process.platform === 'win32') {
        const killer = spawn('taskkill', ['/pid', String(child.pid), '/T', '/F'], { shell: false });
        killer.on('error', () => child.kill());
      } else {
        try { process.kill(-child.pid, 'SIGTERM'); } catch { child.kill(); }
      }
    };
    const cancel = token.onCancellationRequested(kill);
    if (token.isCancellationRequested) kill();
    child.on('error', error => { cancel.dispose(); reject(error); });
    child.on('close', code => {
      cancel.dispose();
      try {
        const events = stdout.split('\n').filter(line => line.trim()).map(line => JSON.parse(line));
        resolve({ code, events });
      } catch (error) { reject(new Error(`Invalid bork test JSON: ${error.message}`)); }
    });
  });
}

module.exports = { registerTesting, execute };
