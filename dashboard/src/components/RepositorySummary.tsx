import { AlertTriangle, Boxes, Cpu, FileCode2, GitCommit, Network, Package, ShieldCheck } from 'lucide-react'
import type { ReactNode } from 'react'
import type { Plan } from '../types/api'
import { repoName } from '../lib/format'
import { Badge, Card, Mono } from './ui'

function Stat({ icon, label, value, sub }: { icon: ReactNode; label: string; value: ReactNode; sub?: ReactNode }) {
  return (
    <div className="rounded-lg border border-line bg-surface px-3.5 py-3">
      <div className="flex items-center gap-2 text-2xs uppercase tracking-wider text-faint">
        {icon}
        {label}
      </div>
      <div className="mt-1.5 text-[15px] font-medium text-fg truncate">{value}</div>
      {sub && <div className="text-xs text-muted truncate">{sub}</div>}
    </div>
  )
}

export function RepositorySummary({ plan, sourceDir, files }: { plan: Plan; sourceDir?: string; files?: number }) {
  const s = plan.source
  // Local sources are titled by directory name; the full path is shown
  // only in the source line, never as the headline.
  const name = s.repository ? repoName(s.repository) : sourceDir ? (sourceDir.split('/').filter(Boolean).pop() ?? sourceDir) : 'local directory'
  const level = plan.compatibility.level
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold tracking-tight">{name}</h1>
        <Badge tone={level === 'supported' ? 'ok' : level === 'experimental' ? 'warn' : 'bad'}>{level}</Badge>
        {s.commit && (
          <span className="inline-flex items-center gap-1.5 text-xs text-muted">
            <GitCommit className="size-3.5" aria-hidden /> <Mono>{s.commit.slice(0, 12)}</Mono>
            {s.ref && <span className="text-faint">({s.ref})</span>}
          </span>
        )}
        {files !== undefined && <span className="text-xs text-faint">{files} files read · no code executed</span>}
      </div>
      {s.url && <a href={s.url} target="_blank" rel="noreferrer noopener" className="text-xs text-accent hover:underline font-mono">{s.url}</a>}
      {!s.url && sourceDir && <p className="text-xs text-muted font-mono">local directory · {s.contentHash.slice(0, 19)}</p>}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <Stat icon={<Boxes className="size-3.5" aria-hidden />} label="Framework" value={plan.stack.framework || '—'} sub={plan.stack.language} />
        <Stat icon={<Cpu className="size-3.5" aria-hidden />} label="Runtime" value={`${plan.stack.runtime} ${plan.stack.runtimeVersion}`} sub={<Mono>{plan.image.reference.replace('docker.io/library/', '')}</Mono>} />
        <Stat icon={<Package className="size-3.5" aria-hidden />} label="Packages" value={plan.stack.packageManager || '—'}
          sub={plan.stack.packageManagerVersion && plan.stack.packageManagerVersion !== 'bundled' ? `v${plan.stack.packageManagerVersion}` : 'bundled'} />
        <Stat icon={<Network className="size-3.5" aria-hidden />} label="Port" value={plan.ports[0]?.containerPort ?? '—'} sub={plan.readiness[0] ? `ready: GET ${plan.readiness[0].path}` : undefined} />
      </div>
    </div>
  )
}

export function CompatibilityPanel({ plan, violations }: { plan: Plan; violations?: { rule: string; message: string }[] | null }) {
  return (
    <Card title="Compatibility & safety">
      <div className="space-y-3 text-[13px]">
        {!plan.compatibility.runnable ? (
          <div className="rounded-lg bg-bad-soft border border-bad/30 p-3">
            <p className="font-medium text-bad">Not runnable by ZEROSETUP</p>
            <ul className="mt-1 list-disc pl-5 text-fg/90">{plan.compatibility.reasons?.map((r) => <li key={r}>{r}</li>)}</ul>
          </div>
        ) : (
          <p className="flex items-center gap-2 text-ok"><ShieldCheck className="size-4" aria-hidden /> Plan passes the execution policy</p>
        )}
        {violations?.filter((v) => v.rule !== 'runnable').map((v) => (
          <p key={v.message} className="text-bad text-xs">policy: {v.message}</p>
        ))}
        <ul className="text-xs text-muted space-y-1">
          <li>• Non-root (uid 1000), all Linux capabilities dropped, no-new-privileges, read-only root filesystem</li>
          <li>• No host directories mounted; source is copied into a ZEROSETUP-owned volume</li>
          <li>• Limits: {plan.resources.cpus} CPU · {plan.resources.memoryMB} MB · {plan.resources.pidsLimit} PIDs</li>
          <li>• Preview published on {plan.network.publishHost} only</li>
          <li className="text-warn">• Outbound network is <strong>not</strong> filtered (dependency installs need it)</li>
        </ul>
      </div>
    </Card>
  )
}

export function WarningsPanel({ plan }: { plan: Plan }) {
  if (!plan.warnings.length && !plan.conflicts.length && !plan.existingConfigs.length) return null
  return (
    <Card title="Warnings, conflicts & existing specs">
      <div className="space-y-3 text-[13px]">
        {plan.warnings.map((w) => (
          <p key={w.code + w.message} className="flex gap-2"><AlertTriangle className="size-4 text-warn shrink-0 mt-0.5" aria-hidden /> <span>{w.message}</span></p>
        ))}
        {plan.conflicts.map((c) => (
          <div key={c.field + c.signals.join()} className="rounded-lg border border-warn/30 bg-warn-soft p-3">
            <p className="font-medium text-warn">Conflicting signals: {c.field}</p>
            <p className="font-mono text-xs mt-1">{c.signals.join('  vs  ')}</p>
            <p className="text-xs text-muted mt-1">{c.resolution}</p>
          </div>
        ))}
        {plan.existingConfigs.map((ec) => (
          <div key={ec.file} className="rounded-lg border border-line p-3">
            <p className="flex items-center gap-2"><FileCode2 className="size-4 text-muted" aria-hidden /><Mono>{ec.file}</Mono><Badge>{ec.kind}</Badge><Badge tone="muted">not executed</Badge></p>
            <p className="text-xs text-muted mt-1">{ec.reason}</p>
            {ec.findings?.length ? (
              <ul className="mt-2 space-y-0.5 text-xs">
                {ec.findings.map((f) => <li key={f} className={f.startsWith('UNSAFE') ? 'text-bad' : 'text-fg/80'}>{f}</li>)}
              </ul>
            ) : null}
          </div>
        ))}
      </div>
    </Card>
  )
}
