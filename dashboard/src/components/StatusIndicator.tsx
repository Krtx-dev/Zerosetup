import type { SessionState } from '../types/api'
import { stateTone } from '../lib/format'
import { Dot, cx } from './ui'

const labels: Partial<Record<SessionState, string>> = {
  AWAITING_APPROVAL: 'Awaiting approval',
  PREPARING: 'Preparing',
  BUILDING: 'Installing',
  STARTING: 'Starting',
  VERIFYING: 'Verifying',
  READY: 'Ready',
  FAILED: 'Failed',
  STOPPED: 'Stopped',
  CANCELLED: 'Cancelled',
  REMOVED: 'Removed',
  UNKNOWN: 'Unknown',
}

export function StatusIndicator({ state, className }: { state: SessionState; className?: string }) {
  const tone = stateTone(state)
  const color = { ok: 'text-ok', warn: 'text-warn', bad: 'text-bad', info: 'text-accent', busy: 'text-accent', muted: 'text-muted' }[tone]
  return (
    <span className={cx('inline-flex items-center gap-2 text-[12px] font-medium', color, className)} aria-label={`State: ${state}`}>
      <Dot tone={tone} />
      {labels[state] ?? state}
    </span>
  )
}
