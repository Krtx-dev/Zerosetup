import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { RepositoryInput } from './RepositoryInput'
import { DetectionEvidence } from './DetectionEvidence'
import { StatusIndicator } from './StatusIndicator'

describe('RepositoryInput', () => {
  it('validates before submitting and submits trimmed input', async () => {
    const onSubmit = vi.fn()
    render(<RepositoryInput onSubmit={onSubmit} />)
    const input = screen.getByLabelText('Repository URL or path')
    await userEvent.type(input, 'http://github.com/a/b')
    await userEvent.click(screen.getByRole('button', { name: /inspect repository/i }))
    expect(onSubmit).not.toHaveBeenCalled()
    expect(screen.getByRole('alert')).toHaveTextContent(/https/)
    await userEvent.clear(input)
    await userEvent.type(input, '  https://github.com/vitejs/vite  {enter}')
    expect(onSubmit).toHaveBeenCalledWith('https://github.com/vitejs/vite', undefined)
  })
})

describe('DetectionEvidence', () => {
  it('shows provenance and expands evidence via keyboard', async () => {
    render(<DetectionEvidence decisions={[{ field: 'packageManager', value: 'pnpm', kind: 'DETECTED', certainty: 'high', reason: 'pnpm selected because pnpm-lock.yaml exists', evidence: [{ file: 'pnpm-lock.yaml', detail: 'lockfile' }] }]} />)
    expect(screen.getByText('Detected')).toBeInTheDocument()
    const row = screen.getByRole('button', { name: /package manager/i })
    expect(row).toHaveAttribute('aria-expanded', 'false')
    row.focus()
    await userEvent.keyboard('{Enter}')
    expect(row).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText(/pnpm-lock.yaml exists/)).toBeInTheDocument()
  })
})

describe('StatusIndicator', () => {
  it('exposes state accessibly', () => {
    render(<StatusIndicator state="AWAITING_APPROVAL" />)
    expect(screen.getByLabelText('State: AWAITING_APPROVAL')).toHaveTextContent('Awaiting approval')
  })
})
