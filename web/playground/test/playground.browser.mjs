import { test, expect } from '@playwright/test';

test('Wasm checker, fallback proofs, Unicode types and formatting work without a backend', async ({ page }) => {
  const sentSource = [];
  page.on('request', request => {
    if (request.method() !== 'GET') sentSource.push(request.url());
  });
  await page.goto('/try/');
  await page.getByRole('button', { name: 'Check', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Checked successfully', { timeout: 30000 });
  await page.getByLabel('Example', { exact: true }).selectOption('facts');
  await page.getByRole('button', { name: 'Check', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Checked successfully');
  await page.getByLabel('Example', { exact: true }).selectOption('local');
  await page.getByRole('button', { name: 'Check', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Needs local bork');
  await expect(page.locator('#diagnostics')).toContainText('compile-time predicate execution');
  await page.getByLabel('Example', { exact: true }).selectOption('effects');
  await page.getByRole('button', { name: 'Check', exact: true }).click();
  await expect(page.locator('#diagnostics')).toContainText('uses io');
  const editor = page.getByLabel('playground.bork', { exact: true });
  const source = 'fn main() { text = "å😀"; println(text) }';
  await editor.fill(source);
  await editor.evaluate((element, offset) => element.setSelectionRange(offset, offset), source.lastIndexOf('text'));
  await page.getByRole('button', { name: 'Inspect type' }).click();
  await expect(page.locator('#type')).toContainText('String');
  await editor.fill('fn main(){println(1)}');
  await page.getByRole('button', { name: 'Format', exact: true }).click();
  await expect(editor).toHaveValue('fn main() { println(1) }\n');
  expect(sentSource).toEqual([]);
});

test('diagnostics jump to source and source downloads remain local', async ({ page }) => {
  await page.goto('/try/');
  const editor = page.getByLabel('playground.bork', { exact: true });
  await editor.fill('fn main() { println(missing) }');
  await page.getByRole('button', { name: 'Check', exact: true }).click();
  const diagnostic = page.locator('#diagnostics button');
  await expect(diagnostic).toContainText('undefined: missing');
  await diagnostic.click();
  expect(await editor.evaluate(element => element.selectionStart)).toBe(20);
  const downloaded = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Download source' }).click();
  expect((await downloaded).suggestedFilename()).toBe('playground.bork');
});

test('no cached types survive an invalid edit and user text stays text', async ({ page }) => {
  await page.goto('/try/');
  const editor = page.getByLabel('playground.bork', { exact: true });
  const source = 'fn main() { x = 1; println(x) }';
  await editor.fill(source);
  await editor.evaluate((element, offset) => element.setSelectionRange(offset, offset), source.lastIndexOf('x'));
  await page.getByRole('button', { name: 'Inspect type' }).click();
  await expect(page.locator('#type')).toContainText('Int');
  await editor.fill('fn main() { println(missing) }');
  await expect(page.locator('#type')).toBeHidden();
  await page.getByRole('button', { name: 'Check', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('found errors');
  await editor.fill('import "<img src=x onerror=alert(1)>"\nfn main() {}');
  await page.getByRole('button', { name: 'Check', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Needs local bork');
  expect(await page.locator('img').count()).toBe(0);
});

test('browsers without gzip decompression use the raw Wasm fallback', async ({ page }) => {
  const wasmRequests = [];
  page.on('request', request => { if (request.url().includes('checker.wasm')) wasmRequests.push(request.url()); });
  await page.route('**/worker.js', async route => {
    const original = await route.fetch();
    await route.fulfill({ response: original, body: 'self.DecompressionStream = undefined;\n' + await original.text() });
  });
  await page.goto('/try/');
  await page.getByRole('button', { name: 'Check', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Checked successfully', { timeout: 30000 });
  expect(wasmRequests).toHaveLength(1);
  expect(wasmRequests[0]).toMatch(/checker\.wasm$/);
});

test('a hung worker is stopped and the next check recovers', async ({ page }) => {
  let first = true;
  await page.route('**/worker.js', async route => {
    if (first) {
      first = false;
      await route.fulfill({ contentType: 'application/javascript', body: 'self.postMessage({ type: "ready" }); self.onmessage = () => {};' });
    } else {
      await route.continue();
    }
  });
  await page.goto('/try/');
  await page.getByRole('button', { name: 'Check', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('timed out');
  await page.getByRole('button', { name: 'Check', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Checked successfully', { timeout: 30000 });
});

test('an edit while checking cannot receive stale success', async ({ page }) => {
  await page.route('**/worker.js', route => route.fulfill({
    contentType: 'application/javascript',
    body: 'self.postMessage({ type: "ready" }); self.onmessage = () => setTimeout(() => self.postMessage({ type: "result", response: { ok: true, diagnostics: [] } }), 200);',
  }));
  await page.goto('/try/');
  const check = page.getByRole('button', { name: 'Check', exact: true });
  await check.click();
  await page.getByLabel('playground.bork', { exact: true }).fill('fn main() { println(missing) }');
  await expect(check).toBeEnabled();
  await expect(page.getByRole('status')).toContainText('Code changed');
});
