import { defineConfig, devices } from '@playwright/test'

// E2E runs against the real Go binary in demo mode with a frozen clock so
// data, forecasts and incidents are identical on every run.
const port = Number(process.env.HP_E2E_PORT ?? 18190)
const clock = process.env.HP_E2E_CLOCK ?? '2026-11-05T12:00:00Z'
const baseURL = `http://127.0.0.1:${port}`
// `make e2e` passes a prebuilt binary; otherwise build one from source.
const bin = process.env.HP_E2E_BIN
const serve = `serve --demo --demo-clock=${clock} --listen 127.0.0.1:${port} --data-dir "$PWD/.e2e/data"`
const command = bin
  ? `sh -c 'rm -rf .e2e && mkdir -p .e2e && "${bin}" ${serve}'`
  : `sh -c 'rm -rf .e2e && mkdir -p .e2e && (cd .. && go build -o web/.e2e/harbor-pilot ./cmd/harbor-pilot) && .e2e/harbor-pilot ${serve}'`

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL,
    trace: 'retain-on-failure',
    timezoneId: 'UTC',
    locale: 'en-GB',
  },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 1000 } } },
    { name: 'mobile', use: { ...devices['Pixel 7'] }, testMatch: /responsive\.spec\.ts/ },
  ],
  webServer: {
    command,
    url: `${baseURL}/readyz`,
    timeout: 120_000,
    reuseExistingServer: !process.env.CI,
    stdout: 'ignore',
    stderr: 'pipe',
  },
})
