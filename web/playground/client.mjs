export const MAX_SOURCE_BYTES = 32 * 1024;

export function sourcePosition(source, offset) {
  const prefix = source.slice(0, offset);
  const lines = prefix.split('\n');
  return { line: lines.length, column: new TextEncoder().encode(lines.at(-1)).length + 1 };
}

export function sourceOffset(source, line, column) {
  const lines = source.split('\n');
  const row = Math.max(0, Math.min(line - 1, lines.length - 1));
  let bytes = 0;
  let chars = 0;
  for (const char of lines[row]) {
    const size = new TextEncoder().encode(char).length;
    if (bytes + size > column - 1) break;
    bytes += size;
    chars += char.length;
  }
  return lines.slice(0, row).reduce((total, text) => total + text.length + 1, 0) + chars;
}

// Each action has a deadline. Terminating the worker also discards its Go
// runtime and typed tree; the next action starts a clean worker.
export class CompilerClient {
  constructor(workerURL, { WorkerClass = Worker, loadTimeout = 30000, requestTimeout = 3000 } = {}) {
    this.workerURL = workerURL;
    this.WorkerClass = WorkerClass;
    this.loadTimeout = loadTimeout;
    this.requestTimeout = requestTimeout;
    this.worker = null;
    this.pending = null;
  }

  async start() {
    if (this.worker) return this.loading;
    const worker = new this.WorkerClass(this.workerURL);
    this.worker = worker;
    this.loading = new Promise((resolve, reject) => {
      const timer = setTimeout(() => this.fail('Compiler loading timed out. Try again.', reject), this.loadTimeout);
      worker.onmessage = ({ data }) => {
        if (this.worker !== worker) return;
        if (data.type === 'ready') {
          clearTimeout(timer);
          resolve();
        } else if (data.type === 'failure') {
          clearTimeout(timer);
          this.fail(data.message, reject);
        } else if (data.type === 'result' && this.pending) {
          clearTimeout(this.pending.timer);
          const { resolve } = this.pending;
          this.pending = null;
          resolve(data.response);
        }
      };
      worker.onerror = () => {
        if (this.worker !== worker) return;
        clearTimeout(timer);
        this.fail('Compiler worker stopped. Try again.', reject);
      };
    });
    return this.loading;
  }

  fail(message, loadingReject) {
    this.worker?.terminate();
    this.worker = null;
    this.loading = null;
    if (this.pending) {
      clearTimeout(this.pending.timer);
      this.pending.reject(new Error(message));
      this.pending = null;
    }
    loadingReject?.(new Error(message));
  }

  async request(request) {
    if (new TextEncoder().encode(request.source).length > MAX_SOURCE_BYTES) {
      throw new Error('Source exceeds the 32 KiB playground limit. Download it and use local bork.');
    }
    if (this.pending) throw new Error('Wait for the current compiler action.');
    await this.start();
    if (this.pending) throw new Error('Wait for the current compiler action.');
    return new Promise((resolve, reject) => {
      this.pending = {
        resolve, reject,
        timer: setTimeout(() => this.fail('Compiler action timed out. Try a smaller file or use local bork.'), this.requestTimeout),
      };
      this.worker.postMessage({ type: 'request', request });
    });
  }
}
