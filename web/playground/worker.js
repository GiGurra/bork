/* Go and the matching Wasm runtime are copied into one versioned bundle. */
importScripts('wasm_exec.js');

async function initialize() {
  const go = new Go();
  let bytes;
  if (typeof DecompressionStream === 'function') {
    const response = await fetch(new URL('checker.wasm.gz', self.location.href));
    if (!response.ok) throw new Error('Could not download the compiler.');
    bytes = await new Response(response.body.pipeThrough(new DecompressionStream('gzip'))).arrayBuffer();
  } else {
    const response = await fetch(new URL('checker.wasm', self.location.href));
    if (!response.ok) throw new Error('Could not download the compiler.');
    bytes = await response.arrayBuffer();
  }
  const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
  go.run(instance).catch(() => self.postMessage({ type: 'failure', message: 'Compiler runtime stopped. Try again.' }));
  if (typeof self.borkPlayground !== 'function') throw new Error('Compiler did not initialize.');
  self.onmessage = ({ data }) => {
    try {
      const response = JSON.parse(self.borkPlayground(JSON.stringify(data.request)));
      self.postMessage({ type: 'result', response });
    } catch {
      self.postMessage({ type: 'failure', message: 'Compiler action failed. Try again or use local bork.' });
    }
  };
  self.postMessage({ type: 'ready' });
}
initialize().catch(error => self.postMessage({ type: 'failure', message: error.message }));
