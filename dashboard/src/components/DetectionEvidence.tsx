import { ChevronRight, FileText } from 'lucide-react'
import { useState } from 'react'
import type { Decision, Kind } from '../types/api'
import { kindLabel } from '../lib/format'
import { Badge, cx } from './ui'

const kindTone: Record<Kind, 'ok' | 'info' | 'warn' | 'bad' | 'muted'> = {
  DETECTED: 'ok',
  INFERRED: 'muted',
  USER_OVERRIDE: 'info',
  DEFAULT: 'warn',
  UNSUPPORTED: 'bad',
}

export function KindBadge({ kind }: { kind: Kind }) {
  return <Badge tone={kindTone[kind]}>{kindLabel[kind]}</Badge>
}

const fieldLabel: Record<string, string> = {
  language: 'Language',
  framework: 'Framework',
  runtimeVersion: 'Runtime',
  packageManager: 'Package manager',
  start: 'Start command',
  port: 'Port',
  readiness: 'Readiness',
  env: 'Environment',
  service: 'Service',
}

/** Every plan decision with its provenance, certainty and evidence. */
export function DetectionEvidence({ decisions }: { decisions: Decision[] }) {
  const [open, setOpen] = useState<number | null>(null)
  return (
    <ul className="divide-y divide-line">
      {decisions.map((d, i) => {
        const expanded = open === i
        const hasEvidence = (d.evidence?.length ?? 0) > 0
        return (
          <li key={`${d.field}-${i}`}>
            <button
              type="button"
              onClick={() => setOpen(expanded ? null : i)}
              aria-expanded={expanded}
              className="w-full grid grid-cols-[140px_1fr_auto] items-center gap-3 px-4 py-2.5 text-left hover:bg-raised/60"
            >
              <span className="text-[13px] text-muted flex items-center gap-1">
                <ChevronRight className={cx('size-3.5 transition-transform', expanded && 'rotate-90', !hasEvidence && 'opacity-30')} aria-hidden />
                {fieldLabel[d.field] ?? d.field}
              </span>
              <span className="font-mono text-[12px] truncate" title={d.value}>{d.value || '—'}</span>
              <span className="flex items-center gap-2">
                <span className="text-2xs text-faint">{d.certainty}</span>
                <KindBadge kind={d.kind} />
              </span>
            </button>
            {expanded && (
              <div className="px-4 pb-3 pl-[164px] space-y-2">
                <p className="text-[13px] text-fg/90">{d.reason}</p>
                {d.evidence?.map((e, j) => (
                  <div key={j} className="flex items-start gap-2 text-xs text-muted">
                    <FileText className="size-3.5 mt-0.5 shrink-0" aria-hidden />
                    <span>
                      {e.file && <span className="font-mono text-fg/80">{e.file}</span>}
                      {e.file && ' — '}
                      {e.detail}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </li>
        )
      })}
    </ul>
  )
}
