const vscode = require('vscode');
const { LanguageClient, TransportKind, State } = require('vscode-languageclient/node');

let client;
let stateListener;

async function activate(context) {
  const watchers = [
    vscode.workspace.createFileSystemWatcher('**/*.bork'),
    vscode.workspace.createFileSystemWatcher('**/bork.mod'),
    vscode.workspace.createFileSystemWatcher('**/bork.sum'),
    vscode.workspace.createFileSystemWatcher('**/go-deps.mod'),
    vscode.workspace.createFileSystemWatcher('**/go-deps.sum'),
    vscode.workspace.createFileSystemWatcher('**/go.mod'),
    vscode.workspace.createFileSystemWatcher('**/go.sum'),
  ];
  context.subscriptions.push(...watchers);
  const status = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 0);
  status.name = 'bork Language Server';
  status.command = 'bork.serverStatus';
  const output = vscode.window.createOutputChannel('bork');
  context.subscriptions.push(status, output);
  let state = 'Stopped';
  let version = '';
  let restarting;
  const updateStatus = () => {
    status.text = `bork${version ? ` ${version}` : ''}: ${state}`;
    status.tooltip = 'bork language server: restart or show output';
    if (vscode.window.activeTextEditor?.document.languageId === 'bork') status.show();
    else status.hide();
  };
  context.subscriptions.push(vscode.window.onDidChangeActiveTextEditor(updateStatus));
  const start = async () => {
    state = 'Starting';
    version = '';
    updateStatus();
    const command = vscode.workspace.getConfiguration('bork').get('serverPath', 'bork');
    const next = new LanguageClient('bork', 'bork', {
      command,
      args: ['lsp'],
      transport: TransportKind.stdio,
    }, {
      documentSelector: [{ scheme: 'file', language: 'bork' }],
      synchronize: { fileEvents: watchers },
      outputChannel: output,
    });
    client = next;
    stateListener = next.onDidChangeState(event => {
      if (client !== next) return;
      state = event.newState === State.Running ? 'Running'
        : event.newState === State.Starting ? 'Starting' : 'Stopped';
      version = next.initializeResult?.serverInfo?.version || '';
      updateStatus();
    });
    try {
      await next.start();
      version = next.initializeResult?.serverInfo?.version || '';
      state = 'Running';
    } catch (error) {
      state = 'Failed';
      output.appendLine(`Could not start bork language server (${command}): ${error.message}`);
      await vscode.window.showErrorMessage(
        `Could not start bork language server (${command}). Install bork or set bork.serverPath. ${error.message}`,
      );
    }
    updateStatus();
  };
  const restart = () => {
    if (restarting) return restarting;
    restarting = (async () => {
      try {
        if (client) {
          const previous = client;
          client = undefined;
          stateListener.dispose();
          await previous.dispose();
        }
        await start();
      } catch (error) {
        state = 'Failed';
        updateStatus();
        output.appendLine(`Could not restart bork language server: ${error.message}`);
        await vscode.window.showErrorMessage(`Could not restart bork language server: ${error.message}`);
      }
    })().finally(() => { restarting = undefined; });
    return restarting;
  };
  context.subscriptions.push(
    vscode.commands.registerCommand('bork.restartServer', restart),
    vscode.commands.registerCommand('bork.showOutput', () => output.show()),
    vscode.commands.registerCommand('bork.serverStatus', async () => {
      const selected = await vscode.window.showQuickPick(['Restart language server', 'Show output'], {
        title: status.text,
      });
      if (selected === 'Restart language server') await restart();
      else if (selected === 'Show output') output.show();
    }),
    { dispose() { stateListener?.dispose(); } },
  );
  await start();
}

async function deactivate() {
  if (client) {
    stateListener?.dispose();
    await client.dispose();
    client = undefined;
  }
}

module.exports = { activate, deactivate };
