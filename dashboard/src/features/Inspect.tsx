import { Play, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { api, errorOf } from '../lib/api'
import { useAsync } from '../hooks/useAsync'
import { navigate } from '../app/router'
import type { ApiError, Overrides } from '../types/api'
import { CompatibilityPanel, RepositorySummary, WarningsPanel } from '../components/RepositorySummary'
import { DetectionEvidence } from '../components/DetectionEvidence'
import { EnvironmentGraph } from '../components/EnvironmentGraph'
import { PermissionDialog } from '../components/PermissionDialog'
import { Button, Card, ErrorState, Mono, Spinner, cx } from '../components/ui'
import { repoName } from '../lib/format'

/** Repository Intelligence + Execution Blueprint + approval. */
export function Inspect({ source, gitRef }: { source: string; gitRef?: string }) {
  const [overrides, setOverrides] = useState<Overrides>({})
  const ovKey = JSON.stringify(overrides)
  const res = useAsync((s) => api.inspect({ source, ref: gitRef, overrides }, s), [source, gitRef, ovKey])
  const [tab, setTab] = useState<'evidence' | 'blueprint' | 'plan'>('evidence')
  const [dialog, setDialog] = useState(false)
  const [busy, setBusy] = useState(false)
  const [runErr, setRunErr] = useState<ApiError>()

  if (res.loading && !res.data) {
    return (
      <div className="mx-auto max-w-6xl px-6 py-10 space-y-3">
        <Spinner label={source.includes('://') || source.startsWith('github.com') ? 'Resolving commit and fetching source (hooks disabled)…' : 'Reading repository files…'} />
        <p className="text-xs text-faint">No repository code is executed during inspection.</p>
      </div>
    )
  }
  if (res.error) {
    return (
      <div className="mx-auto max-w-6xl px-6 py-10 space-y-4">
        <ErrorState error={res.error} onRetry={res.reload} />
        <RunOptions value={overrides} onChange={setOverrides} />
      </div>
    )
  }
  const r = res.data!
  const plan = r.plan

  const approve = async (secrets: Record<string, string>) => {
    setBusy(true)
    setRunErr(undefined)
    try {
      const created = await api.createSession({ source, ref: gitRef, overrides })
      if (created.planHash !== r.planHash) {
        // The repository changed between inspection and approval.
        setRunErr({ code: 'ZS-APPROVAL-002', summary: 'The plan changed since you reviewed it', detail: `Reviewed ${r.planShortHash}, now ${created.planShortHash}.`, suggestion: 'Re-inspect and review the new plan.' })
        setDialog(false)
        res.reload()
        return
      }
      await api.approve(created.session.id, created.planHash, secrets)
      navigate({ name: 'session', id: created.session.id, tab: 'overview' })
    } catch (e) {
      setRunErr(errorOf(e))
      setDialog(false)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mx-auto max-w-6xl px-6 py-8 space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <RepositorySummary plan={plan} sourceDir={r.source.dir} files={r.files} />
        <div className="flex items-center gap-2">
          <Button variant="ghost" onClick={res.reload} icon={<RefreshCw className="size-3.5" aria-hidden />}>Re-inspect</Button>
          <Button variant="primary" disabled={!plan.compatibility.runnable || (r.policyViolations?.length ?? 0) > 0} onClick={() => setDialog(true)}
            icon={<Play className="size-3.5" aria-hidden />}>Run…</Button>
        </div>
      </div>
      {runErr && <ErrorState error={runErr} />}

      <div role="tablist" aria-label="Analysis views" className="flex gap-1 border-b border-line">
        {(['evidence', 'blueprint', 'plan'] as const).map((t) => (
          <button key={t} role="tab" aria-selected={tab === t} onClick={() => setTab(t)}
            className={cx('h-9 px-3 text-[13px] -mb-px border-b-2', tab === t ? 'border-accent text-fg' : 'border-transparent text-muted hover:text-fg')}>
            {{ evidence: 'Repository intelligence', blueprint: 'Execution blueprint', plan: 'Plan JSON' }[t]}
          </button>
        ))}
      </div>

      {tab === 'evidence' && (
        <div className="grid lg:grid-cols-[1fr_380px] gap-4">
          <div className="space-y-4">
            <Card title="Detection evidence" padded={false}><DetectionEvidence decisions={plan.decisions} /></Card>
            <Card title="Execution plan">
              <ol className="space-y-2 text-[13px]">
                {plan.steps.map((s, i) => (
                  <li key={s.id} className="flex gap-3">
                    <span className="text-faint w-4 text-right">{i + 1}</span>
                    <div className="min-w-0"><Mono>{s.command.argv.join(' ')}</Mono><p className="text-xs text-muted">{s.reason}{s.needsNetwork ? ' · needs network' : ''}</p></div>
                  </li>
                ))}
                {plan.start.argv.length > 0 && (
                  <li className="flex gap-3"><span className="text-faint w-4 text-right">{plan.steps.length + 1}</span><div><Mono>{plan.start.argv.join(' ')}</Mono><p className="text-xs text-muted">start (long-running)</p></div></li>
                )}
              </ol>
              {plan.env.length > 0 && (
                <div className="mt-4 border-t border-line pt-3">
                  <p className="text-2xs uppercase tracking-wider text-faint mb-2">Environment</p>
                  <ul className="space-y-1 text-xs">{plan.env.map((e) => <li key={e.name}><Mono>{e.name}={e.secret ? '••• (secret, supplied at run)' : e.value}</Mono> <span className="text-faint">{e.kind}</span></li>)}</ul>
                </div>
              )}
            </Card>
            {(r.services?.length ?? 0) > 0 && (
              <Card title="Likely backing services (not provisioned)">
                <ul className="text-[13px] space-y-1">{r.services!.map((s) => <li key={s.kind + s.file}><strong>{s.kind}</strong> <span className="text-muted">— {s.file}: {s.evidence}</span></li>)}</ul>
              </Card>
            )}
          </div>
          <div className="space-y-4">
            <RunOptions value={overrides} onChange={setOverrides} />
            <CompatibilityPanel plan={plan} violations={r.policyViolations} />
            <WarningsPanel plan={plan} />
            <Card title="Plan identity">
              <p className="text-xs text-muted">Approval is bound to this hash. If anything in the plan changes, you will be asked again.</p>
              <p className="mt-2"><Mono className="text-accent text-[13px]">{r.planShortHash}</Mono></p>
              <p className="text-2xs text-faint mt-2">schema {plan.schemaVersion} · inspected in {r.timingMs.inspect ?? 0} ms</p>
            </Card>
          </div>
        </div>
      )}
      {tab === 'blueprint' && <EnvironmentGraph plan={plan} />}
      {tab === 'plan' && (
        <Card title="Canonical plan (hashed)" padded={false}>
          <pre className="max-h-[640px] overflow-auto p-4 font-mono text-[11.5px] text-fg/85">{JSON.stringify(plan, null, 2)}</pre>
        </Card>
      )}

      <PermissionDialog open={dialog} plan={plan} planHash={r.planShortHash} repository={repoName(plan.source.repository ?? r.source.dir)}
        busy={busy} onCancel={() => setDialog(false)} onApprove={approve} />
    </div>
  )
}

/** Closed set of run options. Each change re-inspects and produces a new
 *  plan hash; nothing here can introduce commands, images or mounts. */
function RunOptions({ value, onChange }: { value: Overrides; onChange: (o: Overrides) => void }) {
  const [ws, setWs] = useState(value.workspace ?? '')
  const toggle = (k: 'postgres' | 'redis') => {
    const cur = new Set(value.services ?? [])
    if (cur.has(k)) cur.delete(k)
    else cur.add(k)
    onChange({ ...value, services: cur.size ? [...cur].sort() : undefined })
  }
  return (
    <Card title="Run options">
      <div className="space-y-3 text-[13px]">
        <fieldset className="flex items-center gap-3">
          <legend className="sr-only">Mode</legend>
          <span className="text-muted w-24">Mode</span>
          {(['dev', 'production'] as const).map((m) => (
            <label key={m} className="flex items-center gap-1.5">
              <input type="radio" name="mode" checked={(value.mode ?? 'dev') === m} onChange={() => onChange({ ...value, mode: m === 'dev' ? undefined : m })} />
              {m}
            </label>
          ))}
        </fieldset>
        <fieldset className="flex items-center gap-3">
          <legend className="sr-only">Managed services</legend>
          <span className="text-muted w-24">Services</span>
          {(['postgres', 'redis'] as const).map((k) => (
            <label key={k} className="flex items-center gap-1.5">
              <input type="checkbox" checked={value.services?.includes(k) ?? false} onChange={() => toggle(k)} />
              {k}
            </label>
          ))}
        </fieldset>
        <form className="flex items-center gap-3" onSubmit={(e) => { e.preventDefault(); onChange({ ...value, workspace: ws.trim() || undefined }) }}>
          <label htmlFor="ws-input" className="text-muted w-24">Workspace</label>
          <input id="ws-input" value={ws} onChange={(e) => setWs(e.target.value)} placeholder="apps/web (npm monorepos)"
            className="flex-1 h-8 rounded-md border border-line bg-bg px-2 font-mono text-xs outline-none focus:border-accent/60" />
          <Button size="sm" type="submit">Apply</Button>
        </form>
        <p className="text-2xs text-faint">Changing an option re-inspects and changes the plan hash you approve.</p>
      </div>
    </Card>
  )
}
