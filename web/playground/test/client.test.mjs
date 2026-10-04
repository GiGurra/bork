import assert from 'node:assert/strict';
import { test } from 'node:test';
import { CompilerClient, sourceOffset, sourcePosition, MAX_SOURCE_BYTES } from '../client.mjs';

class FakeWorker {
  static instances = [];
  constructor() {
    FakeWorker.instances.push(this);
    setTimeout(() => this.onmessage({ data: { type: 'ready' } }), 0);
  }
  terminate() { this.terminated = true; }
  postMessage(message) { this.lastMessage = message; }
  reply(response) { this.onmessage({ data: { type: 'result', response } }); }
}

test('Unicode positions convert UTF-16 cursors to compiler byte columns and back', () => {
  const text = 'first\nå😀 text';
  const offset = text.indexOf('text');
  assert.deepEqual(sourcePosition(text, offset), { line: 2, column: 8 });
  assert.equal(sourceOffset(text, 2, 8), offset);
});

test('worker timeout terminates the runtime and a following action starts cleanly', async () => {
  const client = new CompilerClient('worker.js', { WorkerClass: FakeWorker, requestTimeout: 10 });
  await assert.rejects(client.request({ action: 'check', source: '' }), /timed out/);
  const oldWorker = FakeWorker.instances.at(-1);
  assert.ok(oldWorker.terminated);
  const result = client.request({ action: 'check', source: 'fn main() {}' });
  await new Promise(resolve => setTimeout(resolve, 3));
  const worker = FakeWorker.instances.at(-1);
  assert.notEqual(worker, oldWorker);
  oldWorker.reply({ ok: false });
  worker.reply({ ok: true });
  assert.deepEqual(await result, { ok: true });
});

test('size limit uses bytes and rejects input before loading the compiler', async () => {
  const client = new CompilerClient('worker.js', { WorkerClass: FakeWorker });
  await assert.rejects(client.request({ action: 'check', source: '😀'.repeat(MAX_SOURCE_BYTES / 4 + 1) }), /32 KiB/);
  assert.equal(client.worker, null);
});

test('concurrent actions cannot consume each other’s response during loading', async () => {
  const client = new CompilerClient('worker.js', { WorkerClass: FakeWorker });
  const first = client.request({ action: 'check', source: '' });
  const second = client.request({ action: 'format', source: '' });
  await assert.rejects(second, /current compiler action/);
  FakeWorker.instances.at(-1).reply({ ok: true });
  assert.deepEqual(await first, { ok: true });
});
