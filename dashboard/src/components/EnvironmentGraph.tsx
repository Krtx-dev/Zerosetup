import { Background, Controls, Handle, Position, ReactFlow, type Edge, type Node, type NodeProps } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { CheckCircle2, Circle, Loader2, XCircle, X } from 'lucide-react'
import { useMemo, useState } from 'react'
import type { Facts, Plan, Session } from '../types/api'
import { cx, Mono } from './ui'

type NodeStatus = 'done' | 'running' | 'failed' | 'pending' | 'skipped'

export interface GraphNodeData extends Record<string, unknown> {
  title: string
  subtitle: string
  status: NodeStatus
  purpose: string
  inputs: string[]
  outputs: string[]
  evidence: string[]
  commands: string[]
  error?: string
}

const statusIcon: Record<NodeStatus, React.ReactNode> = {
  done: <CheckCircle2 className="size-3.5 text-ok" aria-hidden />,
  running: <Loader2 className="size-3.5 text-accent animate-spin" aria-hidden />,
  failed: <XCircle className="size-3.5 text-bad" aria-hidden />,
  pending: <Circle className="size-3.5 text-faint" aria-hidden />,
  skipped: <Circle className="size-3.5 text-muted" aria-hidden />,
}

function StepNode({ data, selected }: NodeProps<Node<GraphNodeData>>) {
  return (
    <div
      className={cx(
        'w-[230px] rounded-lg border bg-surface px-3 py-2.5 shadow-sm transition-colors',
        selected ? 'border-accent' : data.status === 'failed' ? 'border-bad/50' : data.status === 'running' ? 'border-accent/50' : 'border-line-strong',
      )}
    >
      <Handle type="target" position={Position.Top} className="!bg-line-strong !border-0 !size-1.5" />
      <div className="flex items-center gap-2">
        {statusIcon[data.status]}
        <span className="text-[12px] font-medium text-fg truncate">{data.title}</span>
      </div>
      <div className="mt-0.5 pl-5.5 font-mono text-[10.5px] text-muted truncate">{data.subtitle}</div>
      <Handle type="source" position={Position.Bottom} className="!bg-line-strong !border-0 !size-1.5" />
    </div>
  )
}

const nodeTypes = { step: StepNode }

/** Derives graph nodes purely from the execution plan and recorded facts. */
export function buildGraph(plan: Plan, session?: Session, facts?: Facts): { nodes: Node<GraphNodeData>[]; edges: Edge[] } {
  const ms = session?.milestones ?? {}
  const failed = session?.state === 'FAILED'
  const stage = session?.error?.stage
  const out: GraphNodeData[] = []
  const src = plan.source
  out.push({
    title: src.kind === 'github' ? 'GitHub repository' : 'Local directory',
    subtitle: src.repository ?? 'local',
    status: 'done',
    purpose: 'The exact source that was inspected.',
    inputs: [src.url ?? src.repository ?? 'local path'],
    outputs: [src.commit ? `commit ${src.commit}` : `content ${src.contentHash.slice(0, 19)}`],
    evidence: src.ref ? [`requested ref: ${src.ref}`] : [],
    commands: [],
  })
  out.push({
    title: 'Source inspector',
    subtitle: `${plan.decisions.length} decisions`,
    status: 'done',
    purpose: 'Reads manifests and configuration without executing any repository code.',
    inputs: plan.decisions.flatMap((d) => d.evidence?.map((e) => e.file ?? '') ?? []).filter((f, i, a) => f && a.indexOf(f) === i),
    outputs: [`${plan.stack.framework} on ${plan.stack.runtime} ${plan.stack.runtimeVersion}`],
    evidence: plan.decisions.map((d) => `${d.field}: ${d.value} (${d.kind}) — ${d.reason}`),
    commands: [],
  })
  out.push({
    title: `${plan.stack.runtime === 'node' ? 'Node.js' : 'Python'} ${plan.stack.runtimeVersion} runtime`,
    subtitle: plan.image.reference.replace('docker.io/library/', ''),
    status: ms.IMAGE_READY ? 'done' : failed && stage === 'image' ? 'failed' : session?.state === 'PREPARING' ? 'running' : 'pending',
    purpose: 'Official runtime image, pulled by tag and recorded by digest.',
    inputs: [plan.image.reference],
    outputs: facts?.image ? [facts.image.digest, `${facts.image.os}/${facts.image.arch}`] : [],
    evidence: plan.decisions.filter((d) => d.field === 'runtimeVersion').map((d) => d.reason),
    commands: [],
  })
  const recs = new Map((facts?.steps ?? []).map((r) => [r.id, r]))
  let firstPendingMarked = false
  for (const st of plan.steps) {
    const r = recs.get(st.id)
    let status: NodeStatus = 'pending'
    if (r?.skipped) status = 'skipped'
    else if (r) status = r.exitCode === 0 ? 'done' : 'failed'
    else if (session?.state === 'BUILDING' && !firstPendingMarked) {
      status = 'running'
      firstPendingMarked = true
    }
    out.push({
      title: st.name,
      subtitle: st.command.argv.join(' '),
      status,
      purpose: st.reason,
      inputs: st.needsNetwork ? ['package registry (network)'] : [],
      outputs: r ? [r.skipped ? `skipped: ${r.skipReason}` : `exit ${r.exitCode} in ${(r.durationMs / 1000).toFixed(1)}s`] : [],
      evidence: [],
      commands: [st.command.argv.join(' ')],
    })
  }
  const svcRecs = new Map((facts?.services ?? []).map((r) => [r.kind, r]))
  const serviceIdx: number[] = []
  for (const sv of plan.services ?? []) {
    const r = svcRecs.get(sv.kind)
    serviceIdx.push(out.length)
    out.push({
      title: `${sv.kind === 'postgres' ? 'PostgreSQL' : 'Redis'} service`,
      subtitle: `${sv.image.replace('docker.io/library/', '')} · ${sv.alias}:${sv.port}`,
      status: r?.readyMs ? 'done' : failed && stage === 'services' ? 'failed' : session?.state === 'STARTING' ? 'running' : 'pending',
      purpose: 'Managed backing service on the session\'s private network; never published to the host.',
      inputs: [sv.image],
      outputs: r ? [r.digest ?? r.image, `ready in ${(r.readyMs / 1000).toFixed(1)}s`] : [`${sv.alias}:${sv.port}`],
      evidence: [sv.reason, `runs as uid ${sv.user}`],
      commands: [sv.readyCommand.join(' ')],
    })
  }
  const listening = facts?.readiness?.listeners?.map((l) => `${l.addr}:${l.port}${l.loopback ? ' (loopback only)' : ''}`) ?? []
  out.push({
    title: 'Application container',
    subtitle: plan.start.argv.join(' '),
    status: ms.PROCESS_STARTED ? (failed && (stage === 'verify' || stage === 'start') && !ms.HTTP_READY ? 'failed' : 'done') : session?.state === 'STARTING' ? 'running' : 'pending',
    purpose: 'Runs the start command as uid 1000 with all capabilities dropped and a read-only root filesystem.',
    inputs: plan.env.map((e) => (e.secret ? `${e.name}=•••` : `${e.name}=${e.value ?? ''}`)),
    outputs: listening.length ? listening : [`port ${plan.ports[0]?.containerPort ?? '?'}`],
    evidence: plan.decisions.filter((d) => d.field === 'start' || d.field === 'port').map((d) => d.reason),
    commands: [plan.start.argv.join(' ')],
  })
  const rc = plan.readiness[0]
  const rd = facts?.readiness
  out.push({
    title: 'HTTP verification',
    subtitle: rc ? `GET ${rc.path} · ${rc.timeoutSec}s` : 'none',
    status: ms.HTTP_READY ? 'done' : failed && stage === 'verify' ? 'failed' : session?.state === 'VERIFYING' ? 'running' : 'pending',
    purpose: 'Confirms a listening socket inside the container, then a real HTTP response through the published port.',
    inputs: rc ? [`container port ${rc.port}`] : [],
    outputs: rd ? [`HTTP ${rd.lastStatus || '—'} after ${rd.attempts} attempts`, rd.healthy ? 'healthy' : 'not confirmed healthy'] : [],
    evidence: rd?.failedReason ? [rd.failedReason] : [],
    commands: [],
    error: rd?.failedReason,
  })
  out.push({
    title: 'Local preview',
    subtitle: session?.url ?? '127.0.0.1 (assigned at start)',
    status: session?.state === 'READY' ? 'done' : 'pending',
    purpose: 'Published only on the loopback interface of this machine.',
    inputs: [],
    outputs: session?.url ? [session.url] : [],
    evidence: [],
    commands: [],
  })
  // Services sit beside the main chain and connect to the application node.
  const appIdx = out.findIndex((d) => d.title === 'Application container')
  let y = 0
  const nodes: Node<GraphNodeData>[] = out.map((d, i) => {
    const isSvc = serviceIdx.includes(i)
    const pos = isSvc ? { x: 280, y: (appIdx - serviceIdx.length + serviceIdx.indexOf(i)) * 92 } : { x: 0, y: y++ * 92 }
    return { id: String(i), type: 'step', position: pos, data: d }
  })
  const chain = out.map((_, i) => i).filter((i) => !serviceIdx.includes(i))
  const edges: Edge[] = chain.slice(1).map((target, k) => ({
    id: `e${k}`,
    source: String(chain[k]),
    target: String(target),
    animated: out[target]!.status === 'running',
  }))
  for (const i of serviceIdx) edges.push({ id: `s${i}`, source: String(i), target: String(appIdx), animated: out[i]!.status === 'running' })
  return { nodes, edges }
}

export function EnvironmentGraph({ plan, session, facts }: { plan: Plan; session?: Session; facts?: Facts }) {
  const { nodes, edges } = useMemo(() => buildGraph(plan, session, facts), [plan, session, facts])
  const [selected, setSelected] = useState<GraphNodeData | null>(null)
  return (
    <div className="grid lg:grid-cols-[1fr_340px] gap-4">
      <div className="h-[560px] rounded-[var(--radius-card)] border border-line bg-surface" aria-label="Execution blueprint graph">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          fitView
          fitViewOptions={{ padding: 0.2 }}
          nodesDraggable={false}
          nodesConnectable={false}
          onNodeClick={(_, n) => setSelected(n.data)}
          proOptions={{ hideAttribution: false }}
        >
          <Background gap={20} size={1} color="var(--zs-line)" />
          <Controls showInteractive={false} />
        </ReactFlow>
      </div>
      <aside className="rounded-[var(--radius-card)] border border-line bg-surface p-4 text-[13px] h-[560px] overflow-auto">
        {!selected ? (
          <p className="text-muted">Select a node to see its purpose, inputs, outputs, evidence and commands. All data comes from the execution plan and recorded facts.</p>
        ) : (
          <div className="space-y-4">
            <div className="flex items-start justify-between gap-2">
              <div>
                <p className="font-medium flex items-center gap-2">{statusIcon[selected.status]} {selected.title}</p>
                <p className="text-xs text-muted mt-1">{selected.purpose}</p>
              </div>
              <button type="button" onClick={() => setSelected(null)} aria-label="Close details" className="text-muted hover:text-fg"><X className="size-4" /></button>
            </div>
            {selected.error && <p className="text-bad text-xs">{selected.error}</p>}
            {(['inputs', 'outputs', 'commands', 'evidence'] as const).map((k) =>
              selected[k].length ? (
                <div key={k}>
                  <p className="text-2xs uppercase tracking-wider text-faint mb-1">{k}</p>
                  <ul className="space-y-1">
                    {selected[k].map((v, i) => <li key={i}>{k === 'commands' ? <Mono>{v}</Mono> : <span className="text-fg/90 break-words">{v}</span>}</li>)}
                  </ul>
                </div>
              ) : null,
            )}
            <p className="text-2xs text-faint">status: {selected.status}</p>
          </div>
        )}
      </aside>
    </div>
  )
}
