import { CompilerClient, sourcePosition, sourceOffset } from './client.mjs';

const examples = {
  hello: 'fn main() {\n  println("Hello from bork!")\n}\n',
  facts: 'pred positive(x: Int) { x > 0 }\n\nfn describe(amount: Int where positive): String {\n  s"sent $amount"\n}\n\nfn transfer(amount: Int): String {\n  if positive(amount) { describe(amount) } else { "nothing to send" }\n}\n\nfn main() {\n  println(transfer(42))\n}\n',
  unions: 'type User = { name: String }\ntype NotFound = { id: Int }\n\nfn find(id: Int): User | NotFound {\n  if id == 1 { User { name: "Ada" } } else { NotFound { id: id } }\n}\n\nfn main() {\n  match find(1) {\n    user: User => println(user.name)\n    missing: NotFound => println(s"No user ${missing.id}")\n  }\n}\n',
  effects: 'fn greeting() {\n  println("Hello!")\n}\n\nfn main() {\n  greeting()\n}\n',
  local: 'pred positive(x: Int) { x > 0 }\n\nfn describe(amount: Int where positive): String {\n  s"sent $amount"\n}\n\nfn main() {\n  println(describe(42))\n}\n',
};
const source = document.querySelector('#source');
const status = document.querySelector('#status');
const diagnostics = document.querySelector('#diagnostics');
const type = document.querySelector('#type');
const actions = ['check', 'format', 'describe'].map(id => document.getElementById(id));
let compiler;
let busy = false;
source.value = examples.hello;

function clearResults() {
  diagnostics.replaceChildren();
  type.hidden = true;
  type.textContent = '';
}
source.addEventListener('input', () => {
  clearResults();
  status.textContent = 'Code changed. Check again for current diagnostics and types.';
});
document.querySelector('#example').addEventListener('change', event => {
  source.value = examples[event.target.value];
  source.dispatchEvent(new Event('input'));
});

async function getCompiler() {
  if (compiler) return compiler;
  const controller = new AbortController();
  const deadline = setTimeout(() => controller.abort(), 15000);
  try {
    const response = await fetch('build/manifest.json', { cache: 'no-store', signal: controller.signal });
    if (!response.ok) throw new Error('Compiler assets are unavailable. Use local bork or try again.');
    const manifest = await response.json();
    compiler = new CompilerClient(new URL(manifest.worker, location.href));
  } catch (error) {
    if (controller.signal.aborted) throw new Error('Compiler asset loading timed out. Try again or use local bork.');
    throw error;
  } finally {
    clearTimeout(deadline);
  }
  return compiler;
}

async function perform(action) {
  if (busy) return;
  busy = true;
  actions.forEach(button => { button.disabled = true; });
  document.querySelector('#example').disabled = true;
  clearResults();
  const text = source.value;
  const cursor = sourcePosition(text, source.selectionStart);
  status.textContent = 'Loading compiler and checking…';
  try {
    const client = await getCompiler();
    const result = await client.request({ action, source: text, ...cursor });
    // An edit during the request must never receive diagnostics from older code.
    if (source.value !== text) return;
    if (result.error) throw new Error(result.error);
    for (const diagnostic of result.diagnostics ?? []) {
      const item = document.createElement('li');
      const button = document.createElement('button');
      button.type = 'button';
      button.textContent = `${diagnostic.line}:${diagnostic.column} · ${diagnostic.code} · ${diagnostic.message}`;
      button.addEventListener('click', () => {
        const offset = sourceOffset(text, diagnostic.line, diagnostic.column);
        source.focus();
        source.setSelectionRange(offset, offset);
      });
      item.append(button);
      diagnostics.append(item);
    }
    if (action === 'format' && result.ok) {
      source.value = result.source ?? '';
      status.textContent = 'Formatted. Check again for diagnostics.';
    } else if (result.needs_local) {
      status.textContent = 'Needs local bork — this file requires checks unavailable in the browser.';
    } else if (result.ok) {
      status.textContent = 'Checked successfully in the browser. The program has not been run.';
    } else {
      status.textContent = 'The compiler found errors.';
    }
    if (result.description) {
      type.hidden = false;
      type.textContent = `${result.description.expression || 'Expression'}: ${result.description.type}`;
    }
  } catch (error) {
    if (source.value === text) status.textContent = error.message;
  } finally {
    busy = false;
    actions.forEach(button => { button.disabled = false; });
    document.querySelector('#example').disabled = false;
  }
}
for (const button of actions) button.addEventListener('click', () => perform(button.id));
document.querySelector('#download').addEventListener('click', () => {
  const url = URL.createObjectURL(new Blob([source.value], { type: 'text/plain;charset=utf-8' }));
  const link = document.createElement('a');
  link.href = url;
  link.download = 'playground.bork';
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
});
