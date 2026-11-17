import { execFileSync, type ChildProcess } from 'node:child_process'

// Stops the dashboard and removes every session the tests created, even
// when a test failed midway, so no labelled resources are left behind.
export default async function globalTeardown() {
  const p = (globalThis as Record<string, unknown>).__zsProc as ChildProcess | undefined
  p?.kill('SIGTERM')
  const bin = process.env.ZS_E2E_BIN
  const env = { ...process.env, ZEROSETUP_HOME: process.env.ZS_E2E_HOME }
  if (!bin || !env.ZEROSETUP_HOME) return
  const list = JSON.parse(execFileSync(bin, ['ps', '--json'], { env }).toString()) as { id: string }[]
  for (const s of list) execFileSync(bin, ['remove', s.id], { env, stdio: 'inherit' })
}
