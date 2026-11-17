import { Copy, ExternalLink, FileDown, OctagonX, Play, RotateCw, Square, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { api, errorOf } from '../lib/api'
import { useAsync } from '../hooks/useAsync'
import { useSessionEvents } from '../hooks/useSessionEvents'
import { href, navigate } from '../app/router'
import type { ApiError } from '../types/api'
import { isExecuting, repoName, shortHash } from '../lib/format'
import { StatusIndicator } from '../components/StatusIndicator'
import { ExecutionTimeline } from '../components/ExecutionTimeline'
import { LiveLogViewer } from '../components/LiveLogViewer'
import { EnvironmentGraph } from '../components/EnvironmentGraph'
import { DiagnosticsPanel, ReadinessPanel, ResourceMetrics } from '../components/panels'
import { PassportViewer } from '../components/PassportViewer'
import { PermissionDialog } from '../components/PermissionDialog'
import { Badge, Button, Card, EmptyState, ErrorState, KV, Mono, Spinner, cx } from '../components/ui'

const tabs = ['overview', 'blueprint', 'logs', 'diagnostics', 'passport'] as const

export function SessionWorkspace({ id, tab }: { id: string; tab: string }) {
  const { events, status: streamStatus } = useSessionEvents(id)
  const detail = useAsync((s) => api.session(id, s), [id])
  const lastSeq = useRef(0)
  // Refresh the session whenever a state or milestone event arrives.
  useEffect(() => {
    const relevant = events.filter((e) => e.seq > lastSeq.current && (e.type === 'state' || e.type === 'milestone' || e.type === 'diagnostic'))
    if (events.length) lastSeq.current = events[events.length - 1]!.seq
    if (relevant.length) detail.reload()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [events])
  const ready = detail.data?.session.state === 'READY'
  const metrics = useAsync((s) => api.metrics(id, s), [id, ready], ready ? 5000 : undefined)
  const [busy, setBusy] = useState<string>()
  const [err, setErr] = useState<ApiError>()
  const [approveOpen, setApproveOpen] = useState(false)
  const [copied, setCopied] = useState(false)

  const streams = useMemo(() => (detail.data?.facts.steps ?? []).filter((s) => !s.skipped).map((s) => s.id), [detail.data])
  if (detail.loading && !detail.data) return <div className="p-8"><Spinner /></div>
  if (detail.error) return <div className="mx-auto max-w-6xl px-6 py-8"><ErrorState error={detail.error} onRetry={detail.reload} /></div>
  const { session: s, facts, plan, planShortHash } = detail.data!
  const executing = isExecuting(s.state)

  const act = async (name: string, fn: () => Promise<unknown>) => {
    setBusy(name)
    setErr(undefined)
    try {
      await fn()
      detail.reload()
    } catch (e) {
      setErr(errorOf(e))
    } finally {
      setBusy(undefined)
    }
  }
  const go = (t: string) => navigate({ name: 'session', id, tab: t })

  return (
    <div className="flex flex-col min-h-full">
      <header className="border-b border-line bg-surface/60 px-6 py-4">
        <div className="mx-auto max-w-[1400px] flex flex-wrap items-center gap-x-4 gap-y-2">
          <div className="min-w-0">
            <div className="flex items-center gap-3">
              <h1 className="text-lg font-semibold tracking-tight truncate">{repoName(s.repository)}</h1>
              <StatusIndicator state={s.state} />
              {streamStatus === 'reconnecting' && <Badge tone="warn">reconnecting</Badge>}
            </div>
            <p className="text-xs text-muted mt-0.5">
              {s.framework} · {s.runtime} · <Mono className="text-muted">{s.id}</Mono> · plan <Mono className="text-muted">{planShortHash}</Mono>
              {s.commit && <> · <Mono className="text-muted">{s.commit.slice(0, 12)}</Mono></>}
              {s.attempt > 1 && <> · attempt {s.attempt}</>}
            </p>
          </div>
          <div className="ml-auto flex flex-wrap items-center gap-2">
            {s.state === 'READY' && s.url && (
              <>
                <Button size="sm" onClick={() => window.open(s.url, '_blank', 'noopener,noreferrer')} icon={<ExternalLink className="size-3.5" aria-hidden />}>Open</Button>
                <Button size="sm" variant="ghost" onClick={() => { void navigator.clipboard.writeText(s.url!); setCopied(true); setTimeout(() => setCopied(false), 1500) }}
                  icon={<Copy className="size-3.5" aria-hidden />}>{copied ? 'Copied' : 'Copy URL'}</Button>
              </>
            )}
            {(s.state === 'AWAITING_APPROVAL' || ((s.state === 'FAILED' || s.state === 'CANCELLED') && !s.approvedAt)) && (
              <Button size="sm" variant="primary" onClick={() => setApproveOpen(true)} icon={<Play className="size-3.5" aria-hidden />}>Review & run</Button>
            )}
            {(s.state === 'FAILED' || s.state === 'CANCELLED') && s.approvedAt && (
              <Button size="sm" onClick={() => act('retry', () => api.approve(s.id, s.planHash))} loading={busy === 'retry'} icon={<RotateCw className="size-3.5" aria-hidden />}>Retry</Button>
            )}
            {executing && detail.data!.executingHere && (
              <Button size="sm" variant="danger" onClick={() => act('cancel', () => api.cancel(s.id))} loading={busy === 'cancel'} icon={<OctagonX className="size-3.5" aria-hidden />}>Cancel</Button>
            )}
            {(s.state === 'READY' || s.state === 'STOPPED') && (
              <Button size="sm" onClick={() => act('restart', () => api.restart(s.id))} loading={busy === 'restart'} icon={<RotateCw className="size-3.5" aria-hidden />}>Restart</Button>
            )}
            {s.state === 'READY' && (
              <Button size="sm" onClick={() => act('stop', () => api.stop(s.id))} loading={busy === 'stop'} icon={<Square className="size-3.5" aria-hidden />}>Stop</Button>
            )}
            <Button size="sm" variant="ghost" onClick={() => go('passport')} icon={<FileDown className="size-3.5" aria-hidden />}>Passport</Button>
            {s.state !== 'REMOVED' && !executing && (
              <Button size="sm" variant="danger" loading={busy === 'remove'} icon={<Trash2 className="size-3.5" aria-hidden />}
                onClick={() => { if (confirm(`Remove all containers, volumes and networks of ${s.id}?`)) void act('remove', () => api.remove(s.id)) }}>Remove</Button>
            )}
          </div>
        </div>
        <nav role="tablist" aria-label="Session views" className="mx-auto max-w-[1400px] mt-3 -mb-4 flex gap-1">
          {tabs.map((t) => (
            <a key={t} role="tab" aria-selected={tab === t} href={href({ name: 'session', id, tab: t })}
              className={cx('h-9 inline-flex items-center px-3 text-[13px] border-b-2 capitalize', tab === t ? 'border-accent text-fg' : 'border-transparent text-muted hover:text-fg')}>
              {t}
              {t === 'diagnostics' && (facts.diagnostics?.length ?? 0) > 0 && <span className="ml-1.5 rounded bg-bad-soft text-bad px-1 text-2xs">{facts.diagnostics!.length}</span>}
            </a>
          ))}
        </nav>
      </header>

      <div className="flex-1 mx-auto w-full max-w-[1400px] px-6 py-6 space-y-4">
        {err && <ErrorState error={err} />}
        {s.error && s.state !== 'READY' && tab === 'overview' && (
          <ErrorState error={s.error} />
        )}

        {tab === 'overview' && (
          <div className="grid xl:grid-cols-[280px_1fr_320px] lg:grid-cols-[280px_1fr] gap-4">
            <div className="space-y-4">
              <Card title="Execution">
                <ExecutionTimeline session={s} facts={facts} hasBuildStep={plan.steps.some((x) => x.stage === 'build')} events={events} />
              </Card>
              <Card title="Services">
                <div className="text-[13px] space-y-2">
                  <div className="flex items-center justify-between"><span>app</span><StatusIndicator state={s.state} /></div>
                  <p className="text-xs text-muted">Container port {plan.ports[0]?.containerPort} → {s.hostPort ? <Mono>127.0.0.1:{s.hostPort}</Mono> : 'not published'}</p>
                  {(plan.services ?? []).map((sv) => {
                    const r = facts.services?.find((x) => x.kind === sv.kind)
                    return (
                      <div key={sv.kind} className="flex items-center justify-between">
                        <span>{sv.kind} <span className="text-2xs text-faint font-mono">{sv.alias}:{sv.port}</span></span>
                        <span className="text-xs text-muted">{r?.readyMs ? 'ready' : 'not running'}</span>
                      </div>
                    )
                  })}
                  {(plan.services ?? []).length === 0 && <p className="text-2xs text-faint">No managed services in this plan.</p>}
                  {(plan.services ?? []).length > 0 && <p className="text-2xs text-faint">Services are reachable only from the app, on the private session network.</p>}
                </div>
              </Card>
            </div>
            <div className="space-y-4 min-w-0">
              <Card title="Preview" padded={false} actions={s.url && s.state === 'READY' ? <Mono className="text-muted">{s.url}</Mono> : undefined}>
                {s.state === 'READY' && s.url ? (
                  <iframe
                    title={`Preview of ${repoName(s.repository)}`}
                    src={s.url}
                    sandbox="allow-scripts allow-forms allow-same-origin allow-popups"
                    referrerPolicy="no-referrer"
                    className="w-full h-[460px] rounded-b-[var(--radius-card)] bg-white"
                  />
                ) : (
                  <EmptyState title={executing ? 'Building environment…' : 'No live preview'}>
                    {executing ? 'The preview appears once readiness checks pass.' : s.state === 'STOPPED' ? 'The application is stopped. Restart it to preview.' : 'Preview is available only while the session is READY.'}
                  </EmptyState>
                )}
              </Card>
              <Card title="Logs" padded={false} className="h-[340px] flex flex-col">
                <div className="flex-1 min-h-0">
                  <LiveLogViewer sessionId={id} events={events} streams={streams} appAvailable={!!s.containerId} live={s.state === 'READY' || executing} />
                </div>
              </Card>
            </div>
            <div className="space-y-4 lg:col-span-2 xl:col-span-1">
              <Card title="Readiness"><ReadinessPanel facts={facts} session={s} /></Card>
              <Card title="Resources"><ResourceMetrics metrics={metrics.data} loading={metrics.loading} /></Card>
              <Card title="Environment">
                <dl className="text-[13px]">
                  <KV label="Image"><Mono className="break-all">{facts.image?.digest ?? plan.image.reference}</Mono></KV>
                  <KV label="Engine">{facts.engine ? `${facts.engine.backend} ${facts.engine.serverVersion} (${facts.engine.arch})` : '—'}</KV>
                  <KV label="Cache">{facts.cache ? (facts.cache.depsReused ? 'dependencies reused' : facts.cache.volumeExisted ? 'warm package cache' : 'cold cache') : '—'}</KV>
                  {facts.cache?.reason && <p className="text-2xs text-faint">{facts.cache.reason}</p>}
                  <KV label="Limits">{plan.resources.cpus} CPU · {plan.resources.memoryMB} MB</KV>
                </dl>
              </Card>
            </div>
          </div>
        )}
        {tab === 'blueprint' && <EnvironmentGraph plan={plan} session={s} facts={facts} />}
        {tab === 'logs' && (
          <Card padded={false} className="h-[70vh] flex flex-col">
            <div className="flex-1 min-h-0"><LiveLogViewer sessionId={id} events={events} streams={streams} appAvailable={!!s.containerId} live={s.state === 'READY' || executing} /></div>
          </Card>
        )}
        {tab === 'diagnostics' && <DiagnosticsPanel session={s} facts={facts} onChanged={detail.reload} />}
        {tab === 'passport' && <PassportViewer sessionId={id} />}
      </div>

      <PermissionDialog open={approveOpen} plan={plan} planHash={shortHash(s.planHash)} repository={repoName(s.repository)} busy={busy === 'approve'}
        onCancel={() => setApproveOpen(false)}
        onApprove={(secrets) => act('approve', async () => { await api.approve(s.id, s.planHash, secrets); setApproveOpen(false) })} />
    </div>
  )
}
