import { describe, expect, it } from 'vitest'
import { parseSSE } from './events'
import { duration, shortHash, stateTone } from './format'
import { parseRoute, href } from '../app/router'
import { validateSource } from '../components/RepositoryInput'
import { buildTimeline } from '../components/ExecutionTimeline'
import { buildGraph } from '../components/EnvironmentGraph'
import type { Plan, Session } from '../types/api'

describe('validateSource', () => {
  it('accepts supported inputs', () => {
    for (const s of ['https://github.com/a/b', 'github.com/a/b', 'https://github.com/a/b/tree/main/x', '/abs/path', './rel']) {
      expect(validateSource(s), s).toBeNull()
    }
  })
  it('rejects unsupported inputs with a reason', () => {
    expect(validateSource('')).toMatch(/Enter/)
    expect(validateSource('git@github.com:a/b')).toMatch(/SSH/)
    expect(validateSource('http://github.com/a/b')).toMatch(/https/)
    expect(validateSource('https://gitlab.com/a/b')).toMatch(/github\.com/)
    expect(validateSource('https://u:p@github.com/a/b')).toMatch(/credentials/)
    expect(validateSource('https://github.com/a')).toMatch(/repository/)
    expect(validateSource('relative/path')).toMatch(/absolute/)
  })
})

describe('parseSSE', () => {
  it('parses complete frames and keeps the remainder', () => {
    const buf = 'id: 1\nevent: state\ndata: {"seq":1,"type":"state","message":"a"}\n\n: keepalive\n\nid: 2\ndata: {"seq":2'
    const { events, rest } = parseSSE(buf)
    expect(events.map((e) => e.seq)).toEqual([1])
    expect(rest).toBe('id: 2\ndata: {"seq":2')
  })
  it('skips malformed frames', () => {
    expect(parseSSE('data: {nope\n\n').events).toEqual([])
  })
})

describe('router', () => {
  it('round-trips routes and rejects malformed session ids', () => {
    expect(parseRoute('#/sessions/zs1a2b3c4d/logs')).toEqual({ name: 'session', id: 'zs1a2b3c4d', tab: 'logs' })
    expect(parseRoute('#/sessions/../../x')).toEqual({ name: 'home' })
    const r = { name: 'inspect' as const, source: 'https://github.com/a/b', ref: 'v1' }
    expect(parseRoute(href(r))).toEqual(r)
  })
})

describe('format', () => {
  it('formats values', () => {
    expect(shortHash('sha256:0123456789abcdef')).toBe('0123456789ab')
    expect(duration(1500)).toBe('1.5 s')
    expect(duration(65000)).toBe('1m 5s')
    expect(stateTone('READY')).toBe('ok')
    expect(stateTone('FAILED')).toBe('bad')
  })
})

const plan: Plan = {
  schemaVersion: '1.0',
  source: { kind: 'local', contentHash: 'sha256:abc' },
  stack: { language: 'JS', framework: 'Vite', runtime: 'node', runtimeVersion: '24', packageManager: 'npm' },
  image: { reference: 'docker.io/library/node:24-slim' },
  steps: [{ id: 'install-deps', name: 'Install dependencies', stage: 'install', command: { argv: ['npm', 'ci'] }, needsNetwork: true, timeoutSec: 900, reason: '' }],
  start: { argv: ['node_modules/.bin/vite'] },
  ports: [{ name: 'http', containerPort: 5173, protocol: 'http' }],
  env: [],
  readiness: [{ type: 'http', port: 5173, path: '/', timeoutSec: 180 }],
  resources: { cpus: 2, memoryMB: 2048, pidsLimit: 1024, logMaxSizeMB: 10, startTimeoutSec: 180 },
  network: { publishHost: '127.0.0.1', installOutbound: true, runtimeOutbound: true, outboundFiltered: false },
  filesystem: { hostMounts: false, readonlyRootfs: true },
  decisions: [],
  warnings: [],
  conflicts: [],
  existingConfigs: [],
  compatibility: { runnable: true, level: 'supported' },
  overrides: [],
}

const base: Session = {
  id: 'zs1a2b3c4d', state: 'FAILED', source: '.', repository: 'x', framework: 'Vite', runtime: 'node 24', planHash: 'sha256:1',
  approvedAt: '2026-01-01T00:00:00Z', milestones: { IMAGE_READY: '2026-01-01T00:00:01Z', DEPENDENCIES_INSTALLED: '2026-01-01T00:00:05Z', PROCESS_STARTED: '2026-01-01T00:00:06Z' },
  createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:20Z', attempt: 1,
  error: { code: 'ZS-READY-001', summary: 'not ready' },
}

describe('timeline and graph derive only from recorded facts', () => {
  it('marks the first missing milestone as failed and never shows HTTP ready', () => {
    const rows = buildTimeline(base, undefined, false)
    const byKey = Object.fromEntries(rows.map((r) => [r.key, r.status]))
    expect(byKey.PROCESS_STARTED).toBe('done')
    expect(byKey.PORT_LISTENING).toBe('failed')
    expect(byKey.HTTP_READY).toBe('pending')
    expect(rows.find((r) => r.key === 'BUILD_SUCCESS')).toBeUndefined()
  })
  it('builds one node per plan step plus fixed stages', () => {
    const g = buildGraph(plan, { ...base, state: 'READY', url: 'http://127.0.0.1:4317', milestones: { ...base.milestones, HTTP_READY: 'x' } })
    expect(g.nodes).toHaveLength(3 + plan.steps.length + 3)
    expect(g.edges).toHaveLength(g.nodes.length - 1)
    expect(g.nodes.at(-1)!.data.outputs).toEqual(['http://127.0.0.1:4317'])
  })
  it('connects managed services to the application node', () => {
    const withSvc: Plan = { ...plan, services: [{ kind: 'postgres', image: 'docker.io/library/postgres:17-alpine', alias: 'postgres', port: 5432, user: '70:70', dataDir: '/d', readyCommand: ['pg_isready'], reason: 'requested' }] }
    const g = buildGraph(withSvc)
    const svc = g.nodes.find((n) => n.data.title === 'PostgreSQL service')!
    const app = g.nodes.find((n) => n.data.title === 'Application container')!
    expect(g.edges.some((e) => e.source === svc.id && e.target === app.id)).toBe(true)
    expect(g.edges.filter((e) => e.target === svc.id)).toHaveLength(0)
  })
})
