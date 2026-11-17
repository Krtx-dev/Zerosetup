import { CheckCircle2, Circle, Loader2, XCircle } from 'lucide-react'
import { motion, useReducedMotion } from 'motion/react'
import { useEffect, useState } from 'react'
import type { Facts, Milestone, Session, SessionEvent } from '../types/api'
import { duration } from '../lib/format'
import { cx } from './ui'

interface Row {
  key: string
  label: string
  at?: string
  status: 'done' | 'active' | 'failed' | 'pending' | 'skipped'
  detail?: string
}

const milestoneOrder: { m: Milestone; label: string }[] = [
  { m: 'IMAGE_READY', label: 'Image prepared' },
  { m: 'DEPENDENCIES_INSTALLED', label: 'Dependencies installed' },
  { m: 'BUILD_SUCCESS', label: 'Application built' },
  { m: 'PROCESS_STARTED', label: 'Container started' },
  { m: 'PORT_LISTENING', label: 'Port listening' },
  { m: 'HTTP_READY', label: 'HTTP ready' },
  { m: 'APPLICATION_HEALTHY', label: 'Health confirmed' },
]

const activeFor: Partial<Record<string, Milestone>> = {
  PREPARING: 'IMAGE_READY',
  BUILDING: 'DEPENDENCIES_INSTALLED',
  STARTING: 'PROCESS_STARTED',
  VERIFYING: 'PORT_LISTENING',
}

export function buildTimeline(session: Session, facts: Facts | undefined, hasBuildStep: boolean): Row[] {
  const ms = session.milestones ?? {}
  const rows: Row[] = [
    { key: 'fetched', label: 'Repository fetched', at: session.createdAt, status: 'done' },
    { key: 'planned', label: 'Plan validated', at: session.createdAt, status: 'done' },
    { key: 'approved', label: 'Execution approved', at: session.approvedAt, status: session.approvedAt ? 'done' : session.state === 'AWAITING_APPROVAL' ? 'active' : 'pending' },
  ]
  const failed = session.state === 'FAILED' || session.state === 'CANCELLED'
  let active = activeFor[session.state]
  if (session.state === 'VERIFYING' && ms.PORT_LISTENING) active = 'HTTP_READY'
  let failedMarked = false
  for (const { m, label } of milestoneOrder) {
    if (m === 'BUILD_SUCCESS' && !hasBuildStep) continue
    if (m === 'APPLICATION_HEALTHY' && ms.HTTP_READY && !ms.APPLICATION_HEALTHY && session.state === 'READY') {
      rows.push({ key: m, label, status: 'skipped', detail: `HTTP ${facts?.readiness?.lastStatus} at ${facts?.readiness?.path} is not a 2xx/3xx status` })
      continue
    }
    if (ms[m]) rows.push({ key: m, label, at: ms[m], status: 'done' })
    else if (failed && !failedMarked && session.approvedAt) {
      failedMarked = true
      rows.push({ key: m, label, status: 'failed', detail: session.error?.summary })
    } else rows.push({ key: m, label, status: active === m ? 'active' : 'pending' })
  }
  return rows
}

const icon = {
  done: <CheckCircle2 className="size-4 text-ok" aria-hidden />,
  active: <Loader2 className="size-4 text-accent animate-spin" aria-hidden />,
  failed: <XCircle className="size-4 text-bad" aria-hidden />,
  pending: <Circle className="size-4 text-faint" aria-hidden />,
  skipped: <Circle className="size-4 text-warn" aria-hidden />,
}

export function ExecutionTimeline({ session, facts, hasBuildStep, events }: { session: Session; facts?: Facts; hasBuildStep: boolean; events: SessionEvent[] }) {
  const rows = buildTimeline(session, facts, hasBuildStep)
  const reduce = useReducedMotion()
  const [now, setNow] = useState(() => Date.now())
  const running = rows.some((r) => r.status === 'active')
  useEffect(() => {
    if (!running) return
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [running])
  const start = session.approvedAt ? new Date(session.approvedAt).getTime() : undefined
  const current = [...events].reverse().find((e) => e.type === 'stage' || e.type === 'progress' || e.type === 'milestone')
  return (
    <div>
      {start && (
        <p className="text-xs text-muted mb-3">
          {running ? 'Elapsed' : 'Total'}{' '}
          <span className="font-mono text-fg">{duration((running ? now : new Date(session.updatedAt).getTime()) - start)}</span>
          {running && current && <span className="ml-2 text-faint">· {current.message}</span>}
        </p>
      )}
      <ol className="relative">
        {rows.map((r, i) => (
          <motion.li
            key={r.key}
            initial={reduce ? false : { opacity: 0, x: -4 }}
            animate={{ opacity: 1, x: 0 }}
            transition={{ duration: 0.18, delay: reduce ? 0 : i * 0.02 }}
            className="relative flex gap-3 pb-3 last:pb-0"
          >
            {i < rows.length - 1 && <span className={cx('absolute left-[7.5px] top-5 bottom-0 w-px', r.status === 'done' ? 'bg-ok/40' : 'bg-line')} aria-hidden />}
            <span className="mt-0.5">{icon[r.status]}</span>
            <div className="min-w-0 flex-1">
              <div className="flex items-baseline justify-between gap-3">
                <span className={cx('text-[13px]', r.status === 'pending' ? 'text-faint' : 'text-fg')}>{r.label}</span>
                {r.at && start && r.status === 'done' && r.key !== 'fetched' && r.key !== 'planned' && r.key !== 'approved' && (
                  <span className="font-mono text-2xs text-muted">+{duration(Math.max(0, new Date(r.at).getTime() - start))}</span>
                )}
              </div>
              {r.detail && <p className={cx('text-xs mt-0.5', r.status === 'failed' ? 'text-bad' : 'text-warn')}>{r.detail}</p>}
            </div>
          </motion.li>
        ))}
      </ol>
    </div>
  )
}
