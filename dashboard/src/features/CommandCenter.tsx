import { AlertTriangle, CheckCircle2, Container, PlayCircle, XCircle } from 'lucide-react'
import { api } from '../lib/api'
import { useAsync } from '../hooks/useAsync'
import { navigate } from '../app/router'
import { RepositoryInput } from '../components/RepositoryInput'
import { SessionCard } from '../components/SessionCard'
import { Card, EmptyState, ErrorState, Spinner } from '../components/ui'
import { isExecuting } from '../lib/format'

export function CommandCenter() {
  const sessions = useAsync((s) => api.sessions(false, s), [], 4000)
  const doctor = useAsync((s) => api.doctor(s), [])
  const list = sessions.data?.sessions ?? []
  const running = list.filter((s) => s.state === 'READY' || isExecuting(s.state))
  const failed = list.filter((s) => s.state === 'FAILED' || s.state === 'UNKNOWN')
  const engine = doctor.data?.checks.find((c) => c.name === 'container engine')

  return (
    <div className="mx-auto max-w-6xl px-6 py-10 space-y-10">
      <section className="space-y-5 max-w-3xl">
        <div>
          <h1 className="text-[28px] font-semibold tracking-tight">From GitHub URL to running application.</h1>
          <p className="text-muted mt-2 text-[15px]">
            Inspect a repository without running it, review an explainable plan, then run it in a hardened local container with real readiness checks.
          </p>
        </div>
        <RepositoryInput onSubmit={(source, ref) => navigate({ name: 'inspect', source, ref })} />
      </section>

      <div className="grid lg:grid-cols-3 gap-4">
        <Card title="Container engine">
          {doctor.loading && !doctor.data ? <Spinner label="Checking…" /> : doctor.error ? <ErrorState error={doctor.error} compact onRetry={doctor.reload} /> : (
            <div className="flex items-start gap-3 text-[13px]">
              {engine?.status === 'ok' ? <CheckCircle2 className="size-4 text-ok mt-0.5" aria-hidden /> : <XCircle className="size-4 text-bad mt-0.5" aria-hidden />}
              <div className="min-w-0">
                <p className="font-medium">{engine?.status === 'ok' ? 'Available' : 'Unavailable'}</p>
                <p className="text-xs text-muted break-words">{engine?.detail}</p>
                {engine?.suggestion && <p className="text-xs text-fg/80 mt-1">→ {engine.suggestion}</p>}
                {engine?.status !== 'ok' && <p className="text-xs text-muted mt-1">Inspection still works without Docker.</p>}
              </div>
            </div>
          )}
        </Card>
        <Card title={<span className="flex items-center gap-2"><PlayCircle className="size-4 text-accent" aria-hidden />Running</span>}>
          <p className="text-2xl font-semibold tabular-nums">{sessions.data ? running.length : '—'}</p>
          <p className="text-xs text-muted">ready or executing environments</p>
        </Card>
        <Card title={<span className="flex items-center gap-2"><AlertTriangle className="size-4 text-bad" aria-hidden />Needs attention</span>}>
          <p className="text-2xl font-semibold tabular-nums">{sessions.data ? failed.length : '—'}</p>
          <p className="text-xs text-muted">failed or unreconciled sessions</p>
        </Card>
      </div>

      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-[13px] font-medium text-muted uppercase tracking-wider">Sessions</h2>
          <a href="#/history" className="text-xs text-accent hover:underline">History →</a>
        </div>
        {sessions.error ? <ErrorState error={sessions.error} onRetry={sessions.reload} /> : sessions.loading && !sessions.data ? <Spinner /> : list.length === 0 ? (
          <Card><EmptyState icon={<Container className="size-6" aria-hidden />} title="No sessions yet">Paste a repository URL above to inspect it.</EmptyState></Card>
        ) : (
          <div className="grid md:grid-cols-2 lg:grid-cols-3 gap-3">
            {list.slice(0, 9).map((s) => <SessionCard key={s.id} s={s} />)}
          </div>
        )}
      </section>

      {failed.some((s) => s.error) && (
        <section className="space-y-3">
          <h2 className="text-[13px] font-medium text-muted uppercase tracking-wider">Recent diagnostics</h2>
          <div className="space-y-2">
            {failed.filter((s) => s.error).slice(0, 4).map((s) => (
              <a key={s.id} href={`#/sessions/${s.id}/diagnostics`} className="flex items-center gap-3 rounded-lg border border-line bg-surface px-3.5 py-2.5 text-[13px] hover:border-line-strong">
                <XCircle className="size-4 text-bad shrink-0" aria-hidden />
                <span className="font-mono text-xs text-muted">{s.id}</span>
                <span className="truncate">{s.error!.summary}</span>
                <span className="ml-auto font-mono text-2xs text-faint">{s.error!.code}</span>
              </a>
            ))}
          </div>
        </section>
      )}
    </div>
  )
}
