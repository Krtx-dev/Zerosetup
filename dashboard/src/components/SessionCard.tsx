import { ExternalLink } from 'lucide-react'
import type { Session } from '../types/api'
import { relTime, repoName } from '../lib/format'
import { href } from '../app/router'
import { StatusIndicator } from './StatusIndicator'
import { Mono } from './ui'

export function SessionCard({ s }: { s: Session }) {
  return (
    <a
      href={href({ name: 'session', id: s.id, tab: 'overview' })}
      className="group block rounded-lg border border-line bg-surface px-3.5 py-3 hover:border-line-strong transition-colors"
    >
      <div className="flex items-center justify-between gap-3">
        <span className="text-[13px] font-medium truncate">{repoName(s.repository)}</span>
        <StatusIndicator state={s.state} />
      </div>
      <div className="mt-1.5 flex items-center gap-2 text-xs text-muted">
        <span className="truncate">{s.framework || '—'}</span>
        <span className="text-faint">·</span>
        <Mono className="text-muted">{s.id}</Mono>
        <span className="ml-auto text-faint shrink-0">{relTime(s.updatedAt)}</span>
      </div>
      {s.url && s.state === 'READY' && (
        <span className="mt-2 inline-flex items-center gap-1 text-xs text-accent font-mono">
          {s.url} <ExternalLink className="size-3" aria-hidden />
        </span>
      )}
    </a>
  )
}
