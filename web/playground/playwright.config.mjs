import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './test',
  testMatch: '*.browser.mjs',
  timeout: 45000,
  use: { baseURL: 'http://127.0.0.1:8735', browserName: 'chromium', launchOptions: process.env.BORK_BROWSER_PATH ? { executablePath: process.env.BORK_BROWSER_PATH, args: ['--no-sandbox'] } : {} },
  webServer: {
    command: 'python3 -m http.server 8735 --bind 127.0.0.1 --directory ../../site',
    url: 'http://127.0.0.1:8735/try/',
    reuseExistingServer: !process.env.CI,
  },
});
