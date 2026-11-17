import { Search } from 'lucide-react'
import { useMemo, useState } from 'react'
import { api } from '../lib/api'
import { useAsync } from '../hooks/useAsync'
import { href } from '../app/router'
import type { SessionState } from '../types/api'
import { duration, relTime, repoName } from '../lib/format'
import { StatusIndicator } from '../components/StatusIndicator'
import { Card, EmptyState, ErrorState, Mono, Spinner, cx } from '../components/ui'

const filters: { key: string; label: string; match: (s: SessionState) => boolean }[] = [
  { key: 'all', label: 'All', match: () => true },
  { key: 'running', label: 'Running', match: (s) => ['READY', 'PREPARING', 'BUILDING', 'STARTING', 'VERIFYING'].includes(s) },
  { key: 'ready', label: 'Successful', match: (s) => s === 'READY' || s === 'STOPPED' },
  { key: 'failed', label: 'Failed', match: (s) => s === 'FAILED' || s === 'UNKNOWN' || s === 'CANCELLED' },
  { key: 'removed', label: 'Removed', match: (s) => s === 'REMOVED' },
]

export function History() {
  const { data, error, loading, reload } = useAsync((s) => api.sessions(true, s), [], 8000)
  const [q, setQ] = useState('')
  const [f, setF] = useState('all')
  const rows = useMemo(() => {
    const m = filters.find((x) => x.key === f)!.match
    const qq = q.toLowerCase()
    return (data?.sessions ?? []).filter((s) => m(s.state) && (!qq || [s.repository, s.framework, s.id, s.runtime].some((v) => v.toLowerCase().includes(qq))))
  }, [data, q, f])
  return (
    <div className="mx-auto max-w-6xl px-6 py-8 space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold tracking-tight mr-auto">Session history</h1>
        <div role="tablist" aria-label="Filter sessions" className="flex gap-1">
          {filters.map((x) => (
            <button key={x.key} role="tab" aria-selected={f === x.key} onClick={() => setF(x.key)}
              className={cx('h-8 px-2.5 rounded-md text-xs', f === x.key ? 'bg-raised text-fg' : 'text-muted hover:text-fg')}>{x.label}</button>
          ))}
        </div>
        <label className="flex items-center gap-2 h-8 rounded-md border border-line bg-surface px-2.5">
          <Search className="size-3.5 text-faint" aria-hidden />
          <span className="sr-only">Search sessions</span>
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search repository, framework, id" className="w-56 bg-transparent text-xs outline-none" />
        </label>
      </div>
      {error ? <ErrorState error={error} onRetry={reload} /> : loading && !data ? <Spinner /> : (
        <Card padded={false}>
          {rows.length === 0 ? <EmptyState title="No matching sessions" /> : (
            <div className="overflow-x-auto">
              <table className="w-full text-[13px]">
                <thead>
                  <tr className="text-left text-2xs uppercase tracking-wider text-faint border-b border-line">
                    {['Repository', 'State', 'Framework', 'Runtime', 'Duration', 'Last activity', 'Session'].map((h) => <th key={h} scope="col" className="px-4 py-2 font-medium">{h}</th>)}
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {rows.map((s) => {
                    const ms = Object.values(s.milestones ?? {}).map((t) => new Date(t!).getTime())
                    const dur = s.approvedAt && ms.length ? Math.max(...ms) - new Date(s.approvedAt).getTime() : undefined
                    return (
                      <tr key={s.id} className="hover:bg-raised/50">
                        <td className="px-4 py-2.5"><a className="font-medium hover:text-accent" href={href({ name: 'session', id: s.id, tab: 'overview' })}>{repoName(s.repository)}</a></td>
                        <td className="px-4 py-2.5"><StatusIndicator state={s.state} /></td>
                        <td className="px-4 py-2.5 text-muted">{s.framework}</td>
                        <td className="px-4 py-2.5 text-muted">{s.runtime}</td>
                        <td className="px-4 py-2.5 font-mono text-xs">{dur !== undefined && dur > 0 ? duration(dur) : '—'}</td>
                        <td className="px-4 py-2.5 text-muted">{relTime(s.updatedAt)}</td>
                        <td className="px-4 py-2.5"><Mono className="text-muted">{s.id}</Mono></td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}
        </Card>
      )}
    </div>
  )
}
