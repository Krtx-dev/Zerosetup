import { expect, test } from '@playwright/test'

// Full acceptance flow in the UI: inspect → evidence → approve → live build
// → READY with a working preview → passport → remove. Uses the real engine.
// With ZS_CAPTURE_DIR set, real screenshots of each step are written there
// (used for README/demo assets; never edited or staged).
const capture = async (page: import('@playwright/test').Page, name: string) => {
  const dir = process.env.ZS_CAPTURE_DIR
  if (dir) await page.screenshot({ path: `${dir}/${name}.png` })
}

test('inspect, approve, run and remove the Vite fixture', async ({ page, context }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const base = process.env.ZS_DASHBOARD_URL!
  const fixture = `${process.env.ZS_FIXTURES}/vite`
  await page.goto(base)
  await expect(page.getByText('From GitHub URL to running application.')).toBeVisible()
  await capture(page, '01-command-center')

  const input = page.getByLabel('Repository URL or path')
  await input.fill('http://github.com/a/b')
  await page.getByRole('button', { name: /inspect repository/i }).click()
  await expect(page.getByRole('alert')).toContainText('https')

  await input.fill(fixture)
  await page.getByRole('button', { name: /inspect repository/i }).click()
  await expect(page.getByText('React (Vite)').first()).toBeVisible()
  await expect(page.getByText('Plan passes the execution policy')).toBeVisible()
  await expect(page.getByText('npm selected because package-lock.json exists')).toHaveCount(0) // collapsed until expanded
  await page.getByRole('button', { name: /package manager/i }).click()
  await expect(page.getByText('npm selected because package-lock.json exists')).toBeVisible()
  await capture(page, '02-repository-intelligence')
  await page.getByRole('tab', { name: 'Execution blueprint' }).click()
  await page.getByText('Install dependencies').first().click()
  await capture(page, '03-execution-blueprint')

  await page.getByRole('button', { name: 'Run…' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('button', { name: 'Approve and run' })).toBeDisabled()
  await dialog.getByRole('checkbox').check()
  await capture(page, '04-approval')
  await dialog.getByRole('button', { name: 'Approve and run' }).click()

  await expect(page).toHaveURL(/#\/sessions\/zs[0-9a-f]{8}/)
  await expect(page.getByLabel('State: READY').first()).toBeVisible({ timeout: 5 * 60_000 })
  const preview = page.frameLocator('iframe[title^="Preview of"]')
  await expect(preview.getByText('Hello from fixture-vite')).toBeVisible()
  await expect(page.getByRole('meter', { name: 'Memory' })).toBeVisible({ timeout: 20_000 })
  await capture(page, '05-workspace-ready')
  await page.getByRole('tab', { name: 'logs' }).click()
  await capture(page, '06-logs')

  await page.getByRole('tab', { name: 'passport' }).click()
  await expect(page.getByText('Reproducibility passport')).toBeVisible()
  await expect(page.getByText(/node@sha256:/)).toBeVisible()
  await capture(page, '07-passport')

  page.once('dialog', (d) => d.accept())
  await page.getByRole('button', { name: 'Remove' }).click()
  await expect(page.getByLabel('State: REMOVED').first()).toBeVisible({ timeout: 60_000 })
  void context
})
