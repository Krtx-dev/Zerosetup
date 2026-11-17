import type {
  ApiError, CacheStatus, Config, Diagnostic, DoctorCheck, InspectRequest, InspectResponse, Metrics, Repair,
  Session, SessionDetail,
} from '../types/api'

const TOKEN_KEY = 'zerosetup.token'

/** Reads the access token from the URL fragment (first load) or sessionStorage. */
export function initToken(): string | null {
  const m = window.location.hash.match(/token=([0-9a-f]{64})/)
  if (m?.[1]) {
    try {
      sessionStorage.setItem(TOKEN_KEY, m[1])
    } catch {
      /* storage unavailable: token lives only in memory for this page */
    }
    memToken = m[1]
    history.replaceState(null, '', window.location.pathname + '#/')
    return m[1]
  }
  try {
    memToken = sessionStorage.getItem(TOKEN_KEY)
  } catch {
    /* ignore */
  }
  return memToken
}

let memToken: string | null = null

export function token(): string | null {
  return memToken
}

export class ApiRequestError extends Error {
  readonly status: number
  readonly body: ApiError
  constructor(status: number, body: ApiError) {
    super(body.summary)
    this.status = status
    this.body = body
  }
}

async function request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const headers: Record<string, string> = { Authorization: `Bearer ${memToken ?? ''}` }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  let res: Response
  try {
    res = await fetch(`/api/v1${path}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal })
  } catch (e) {
    if ((e as Error).name === 'AbortError') throw e
    throw new ApiRequestError(0, { code: 'ZS-NETWORK', summary: 'Cannot reach the ZEROSETUP server', suggestion: 'Is `zerosetup dashboard` still running?' })
  }
  const ct = res.headers.get('Content-Type') ?? ''
  if (!res.ok) {
    if (ct.includes('application/json')) {
      const j = (await res.json()) as { error?: ApiError | string }
      if (j.error && typeof j.error === 'object') throw new ApiRequestError(res.status, j.error)
      throw new ApiRequestError(res.status, { code: `HTTP-${res.status}`, summary: String(j.error ?? res.statusText) })
    }
    throw new ApiRequestError(res.status, { code: `HTTP-${res.status}`, summary: (await res.text()) || res.statusText })
  }
  if (ct.includes('application/json')) return (await res.json()) as T
  return (await res.text()) as unknown as T
}

export const api = {
  health: (s?: AbortSignal) => request<{ ok: boolean; version: string }>('GET', '/health', undefined, s),
  doctor: (s?: AbortSignal) => request<{ checks: DoctorCheck[] }>('GET', '/doctor', undefined, s),
  config: (s?: AbortSignal) => request<Config>('GET', '/config', undefined, s),
  inspect: (req: InspectRequest, s?: AbortSignal) => request<InspectResponse>('POST', '/inspect', req, s),
  createSession: (req: InspectRequest, s?: AbortSignal) =>
    request<InspectResponse & { session: Session }>('POST', '/sessions', req, s),
  sessions: (all = false, s?: AbortSignal) => request<{ sessions: Session[] }>('GET', `/sessions${all ? '?all=1' : ''}`, undefined, s),
  session: (id: string, s?: AbortSignal) => request<SessionDetail>('GET', `/sessions/${encodeURIComponent(id)}`, undefined, s),
  approve: (id: string, planHash: string, secrets?: Record<string, string>) =>
    request<{ session: Session }>('POST', `/sessions/${encodeURIComponent(id)}/approve`, { planHash, secrets }),
  cancel: (id: string) => request<unknown>('POST', `/sessions/${encodeURIComponent(id)}/cancel`),
  stop: (id: string) => request<{ session: Session }>('POST', `/sessions/${encodeURIComponent(id)}/stop`),
  restart: (id: string, secrets?: Record<string, string>) =>
    request<unknown>('POST', `/sessions/${encodeURIComponent(id)}/restart`, secrets ? { planHash: '', secrets } : undefined),
  remove: (id: string) => request<{ result: { verified: boolean } }>('DELETE', `/sessions/${encodeURIComponent(id)}`),
  logs: (id: string, stream = 'app', tail = '1000', s?: AbortSignal) =>
    request<string>('GET', `/sessions/${encodeURIComponent(id)}/logs?stream=${encodeURIComponent(stream)}&tail=${tail}`, undefined, s),
  metrics: (id: string, s?: AbortSignal) => request<Metrics>('GET', `/sessions/${encodeURIComponent(id)}/metrics`, undefined, s),
  diagnostics: (id: string, s?: AbortSignal) =>
    request<{ diagnostics: Diagnostic[] | null }>('GET', `/sessions/${encodeURIComponent(id)}/diagnostics`, undefined, s),
  applyRepair: (id: string, rid: string) =>
    request<{ repair: Repair; newPlanHash: string; newPlanShortHash: string; session: Session }>(
      'POST', `/sessions/${encodeURIComponent(id)}/repairs/${encodeURIComponent(rid)}/apply`),
  revertRepair: (id: string) => request<{ session: Session }>('POST', `/sessions/${encodeURIComponent(id)}/repairs/revert`),
  passport: (id: string, s?: AbortSignal) => request<Record<string, unknown>>('GET', `/sessions/${encodeURIComponent(id)}/passport`, undefined, s),
  cache: (s?: AbortSignal) => request<CacheStatus>('GET', '/cache', undefined, s),
  cleanCache: () => request<CacheStatus>('POST', '/cache/clean', {}),
}

export function errorOf(e: unknown): ApiError {
  if (e instanceof ApiRequestError) return e.body
  return { code: 'ZS-CLIENT', summary: e instanceof Error ? e.message : String(e) }
}
