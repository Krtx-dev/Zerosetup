import { Activity, Lightbulb, Undo2, Wrench } from 'lucide-react'
import { useState } from 'react'
import { api, errorOf } from '../lib/api'
import type { ApiError, Diagnostic, Facts, Metrics, Repair, Session } from '../types/api'
import { bytes, duration } from '../lib/format'
import { Badge, Button, Card, EmptyState, ErrorState, KV, Mono } from './ui'

export function ReadinessPanel({ facts, session }: { facts: Facts; session: Session }) {
  const r = facts.readiness
  if (!r) return <EmptyState title="No readiness checks yet">Checks run after the application container starts.</EmptyState>
  const ms = session.milestones ?? {}
  const checks: [string, boolean | undefined][] = [
    ['Process running', !!ms.PROCESS_STARTED],
    ['Port listening (0.0.0.0)', !!ms.PORT_LISTENING],
    ['HTTP responding', r.httpReady],
    ['Healthy status', r.healthy],
  ]
  return (
    <div className="space-y-3 text-[13px]">
      <ul className="grid grid-cols-2 gap-2">
        {checks.map(([l, ok]) => (
          <li key={l} className="flex items-center gap-2 rounded-md border border-line px-2.5 py-1.5">
            <span className={ok ? 'text-ok' : 'text-faint'} aria-hidden>{ok ? '✓' : '○'}</span>
            <span className={ok ? 'text-fg' : 'text-muted'}>{l}</span>
            <span className="sr-only">{ok ? 'passed' : 'not passed'}</span>
          </li>
        ))}
      </ul>
      <dl>
        <KV label="Request"><Mono>GET {r.path}</Mono> → {r.lastStatus || '—'}</KV>
        <KV label="Attempts">{r.attempts} over {duration(r.durationMs)}</KV>
        <KV label="Listening">{r.listeners?.length ? r.listeners.map((l) => <Mono key={`${l.addr}:${l.port}`} className="mr-2">{l.addr}:{l.port}{l.loopback ? ' (loopback)' : ''}</Mono>) : 'none observed'}</KV>
        {r.lastError && <KV label="Last error"><span className="text-bad text-xs">{r.lastError}</span></KV>}
        {r.failedReason && <KV label="Failure"><span className="text-bad">{r.failedReason}</span></KV>}
      </dl>
    </div>
  )
}

export function ResourceMetrics({ metrics, loading }: { metrics?: Metrics; loading?: boolean }) {
  if (!metrics) return null
  const u = metrics.usage
  const timings = (metrics.timings ?? []).filter((t) => t.attempt === Math.max(...(metrics.timings ?? []).map((x) => x.attempt)))
  return (
    <div className="space-y-3 text-[13px]">
      {u ? (
        <div className="grid grid-cols-3 gap-2">
          <Meter label="CPU" value={`${u.cpuPercent.toFixed(1)}%`} frac={Math.min(1, u.cpuPercent / 100)} />
          <Meter label="Memory" value={bytes(u.memoryBytes)} frac={u.memoryLimit ? u.memoryBytes / u.memoryLimit : 0} />
          <Meter label="PIDs" value={String(u.pids)} />
        </div>
      ) : loading ? (
        <p className="text-xs text-muted flex items-center gap-2"><Activity className="size-3.5 animate-pulse" aria-hidden />Sampling usage…</p>
      ) : (
        <p className="text-xs text-muted flex items-center gap-2"><Activity className="size-3.5" aria-hidden />{metrics.usageUnavailable ?? 'Usage unavailable'}</p>
      )}
      {timings.length > 0 && (
        <dl>
          {timings.map((t) => <KV key={t.name} label={t.name.replace('step:', '')}><span className="font-mono">{duration(t.millis)}</span></KV>)}
        </dl>
      )}
    </div>
  )
}

function Meter({ label, value, frac }: { label: string; value: string; frac?: number }) {
  return (
    <div className="rounded-md border border-line px-2.5 py-2">
      <p className="text-2xs uppercase tracking-wider text-faint">{label}</p>
      <p className="font-mono text-[12px] mt-0.5 truncate">{value}</p>
      {frac !== undefined && (
        <div className="mt-1.5 h-1 rounded bg-line" role="meter" aria-label={label} aria-valuenow={Math.round(frac * 100)} aria-valuemin={0} aria-valuemax={100}>
          <div className="h-1 rounded bg-accent" style={{ width: `${Math.round(frac * 100)}%` }} />
        </div>
      )}
    </div>
  )
}

export function RepairProposal({ sessionId, repair, onApplied }: { sessionId: string; repair: Repair; onApplied: () => void }) {
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<ApiError>()
  const apply = async () => {
    setBusy(true)
    try {
      await api.applyRepair(sessionId, repair.id)
      onApplied()
    } catch (e) {
      setErr(errorOf(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="rounded-lg border border-accent/30 bg-accent-soft p-3 space-y-2">
      <p className="flex items-center gap-2 text-[13px] font-medium"><Wrench className="size-4 text-accent" aria-hidden /> Proposed repair: {repair.title}</p>
      <p className="text-xs text-muted">{repair.explanation}</p>
      <table className="text-xs font-mono">
        <tbody>
          {repair.changes.map((c) => (
            <tr key={c.override + (c.key ?? '')}>
              <td className="pr-3 text-muted">{c.override}{c.key ? ` ${c.key}` : ''}</td>
              <td className="pr-2 text-faint line-through">{c.from}</td>
              <td className="text-fg">→ {c.to}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="text-xs text-muted">Impact: {repair.impact}. The repository is not modified; the new plan must be approved before it runs.</p>
      {err && <ErrorState error={err} compact />}
      <Button size="sm" variant="primary" loading={busy} onClick={apply}>Apply to plan</Button>
    </div>
  )
}

export function DiagnosticsPanel({ session, facts, onChanged }: { session: Session; facts: Facts; onChanged: () => void }) {
  const diags: Diagnostic[] = facts.diagnostics ?? []
  const [reverting, setReverting] = useState(false)
  const [err, setErr] = useState<ApiError>()
  const revert = async () => {
    setReverting(true)
    try {
      await api.revertRepair(session.id)
      onChanged()
    } catch (e) {
      setErr(errorOf(e))
    } finally {
      setReverting(false)
    }
  }
  return (
    <div className="space-y-4">
      {session.error && <ErrorState error={session.error} />}
      {(facts.repairsApplied?.length ?? 0) > 0 && (
        <Card title="Applied repairs" actions={session.prevPlanHash && !['PREPARING', 'BUILDING', 'STARTING', 'VERIFYING', 'READY'].includes(session.state) ? (
          <Button size="sm" variant="ghost" loading={reverting} onClick={revert} icon={<Undo2 className="size-3.5" aria-hidden />}>Revert last</Button>
        ) : undefined}>
          <p className="text-[13px]">{facts.repairsApplied!.map((r) => <Mono key={r} className="mr-2">{r}</Mono>)}</p>
          {err && <div className="mt-2"><ErrorState error={err} compact /></div>}
        </Card>
      )}
      {diags.length === 0 ? (
        <EmptyState icon={<Lightbulb className="size-6" aria-hidden />} title="No diagnostics">
          Diagnostics are produced when a stage fails. This session has none.
        </EmptyState>
      ) : (
        diags.map((d) => (
          <Card key={d.id + d.summary} title={<span className="flex items-center gap-2"><Badge tone={d.severity === 'error' ? 'bad' : 'warn'}>{d.severity}</Badge>{d.summary}</span>}
            actions={<span className="text-2xs text-faint font-mono">{d.id} · {d.confidence} confidence · {d.stage}</span>}>
            <dl className="text-[13px]">
              {d.evidence?.length ? <KV label="Evidence"><ul className="space-y-1">{d.evidence.map((e) => <li key={e}><Mono className="break-all">{e}</Mono></li>)}</ul></KV> : null}
              <KV label="Likely cause">{d.likelyCause}</KV>
              <KV label="Resolution">{d.proposedResolution}</KV>
            </dl>
            {d.repair && session.state === 'FAILED' && (
              <div className="mt-3"><RepairProposal sessionId={session.id} repair={d.repair} onApplied={onChanged} /></div>
            )}
          </Card>
        ))
      )}
    </div>
  )
}
