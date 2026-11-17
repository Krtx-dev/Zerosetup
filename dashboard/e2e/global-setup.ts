import { spawn } from 'node:child_process'
import { mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

// Starts the real binary on a random loopback port with an isolated data dir
// and hands the tokenized URL to the tests.
export default async function globalSetup() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..')
  const home = mkdtempSync(join(tmpdir(), 'zs-e2e-'))
  const proc = spawn(join(root, 'bin', 'zerosetup'), ['dashboard', '--port', '0', '--no-open', '--no-color'], {
    env: { ...process.env, ZEROSETUP_HOME: home },
    stdio: ['ignore', 'pipe', 'inherit'],
  })
  const url = await new Promise<string>((resolveUrl, reject) => {
    let out = ''
    const t = setTimeout(() => reject(new Error('dashboard did not start: ' + out)), 20_000)
    proc.stdout!.on('data', (d: Buffer) => {
      out += d.toString()
      const m = out.match(/http:\/\/127\.0\.0\.1:\d+\/#token=[0-9a-f]{64}/)
      if (m) {
        clearTimeout(t)
        resolveUrl(m[0])
      }
    })
  })
  process.env.ZS_DASHBOARD_URL = url
  process.env.ZS_FIXTURES = join(root, 'fixtures')
  process.env.ZS_E2E_HOME = home
  process.env.ZS_E2E_BIN = join(root, 'bin', 'zerosetup')
  writeFileSync(join(home, 'e2e.pid'), String(proc.pid))
  ;(globalThis as Record<string, unknown>).__zsProc = proc
}
