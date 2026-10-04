const vscode = require('vscode');
const { LanguageClient, TransportKind } = require('vscode-languageclient/node');

let client;

async function activate(context) {
  const command = vscode.workspace.getConfiguration('bork').get('serverPath', 'bork');
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
  client = new LanguageClient('bork', 'bork', {
    command,
    args: ['lsp'],
    transport: TransportKind.stdio,
  }, {
    documentSelector: [{ scheme: 'file', language: 'bork' }],
    synchronize: { fileEvents: watchers },
  });
  context.subscriptions.push(client);
  try {
    await client.start();
  } catch (error) {
    await vscode.window.showErrorMessage(
      `Could not start bork language server (${command}). Install bork or set bork.serverPath. ${error.message}`,
    );
  }
}

async function deactivate() {
  if (client) {
    await client.stop();
    client = undefined;
  }
}

module.exports = { activate, deactivate };
