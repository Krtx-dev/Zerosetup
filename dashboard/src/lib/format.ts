import type { Kind, SessionState } from '../types/api'

export function shortHash(h: string | undefined): string {
  if (!h) return ''
  return h.startsWith('sha256:') ? h.slice(7, 19) : h.slice(0, 12)
}

export function duration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)} ms`
  const s = ms / 1000
  if (s < 60) return `${s.toFixed(1)} s`
  const m = Math.floor(s / 60)
  return `${m}m ${Math.round(s % 60)}s`
}

export function bytes(n: number): string {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(1)} GiB`
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MiB`
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1)} KiB`
  return `${n} B`
}

export function relTime(iso: string | undefined, now = Date.now()): string {
  if (!iso) return ''
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return ''
  const d = Math.max(0, now - t) / 1000
  if (d < 60) return `${Math.floor(d)}s ago`
  if (d < 3600) return `${Math.floor(d / 60)}m ago`
  if (d < 172800) return `${Math.floor(d / 3600)}h ago`
  return new Date(iso).toLocaleDateString()
}

export type Tone = 'ok' | 'warn' | 'bad' | 'info' | 'busy' | 'muted'

export function stateTone(s: SessionState): Tone {
  switch (s) {
    case 'READY':
      return 'ok'
    case 'FAILED':
    case 'UNKNOWN':
      return 'bad'
    case 'AWAITING_APPROVAL':
      return 'warn'
    case 'STOPPED':
    case 'CANCELLED':
    case 'REMOVED':
      return 'muted'
    default:
      return 'busy'
  }
}

export function isExecuting(s: SessionState): boolean {
  return s === 'PREPARING' || s === 'BUILDING' || s === 'STARTING' || s === 'VERIFYING'
}

export const kindLabel: Record<Kind, string> = {
  DETECTED: 'Detected',
  INFERRED: 'Inferred',
  USER_OVERRIDE: 'Override',
  DEFAULT: 'Default',
  UNSUPPORTED: 'Unsupported',
}

/** Short display name: owner/repo for GitHub, the directory name for local paths. */
export function repoName(repo: string): string {
  const parts = repo.replace(/\/+$/, '').split('/')
  return repo.startsWith('github.com/') ? parts.slice(-2).join('/') : parts.slice(-1).join('/')
}
