import { CheckCircle2, CircleAlert, Moon, Sun, Trash2, XCircle } from 'lucide-react'
import { useState } from 'react'
import { api, errorOf } from '../lib/api'
import { useAsync } from '../hooks/useAsync'
import type { ApiError } from '../types/api'
import { bytes } from '../lib/format'
import { Button, Card, ErrorState, KV, Mono, Spinner } from '../components/ui'
import { setTheme, theme } from '../app/theme'

/** Settings are read-only where ZEROSETUP has no persistent configuration
 *  yet: security-sensitive defaults cannot be weakened from the UI. */
export function Settings() {
  const doctor = useAsync((s) => api.doctor(s), [])
  const config = useAsync((s) => api.config(s), [])
  const cache = useAsync((s) => api.cache(s), [])
  const [cleaning, setCleaning] = useState(false)
  const [cleanErr, setCleanErr] = useState<ApiError>()
  const [th, setTh] = useState(theme())
  const clean = async () => {
    if (!confirm('Remove ZEROSETUP package cache volumes? Running sessions keep working; the next install will be slower.')) return
    setCleaning(true)
    setCleanErr(undefined)
    try {
      await api.cleanCache()
      cache.reload()
    } catch (e) {
      setCleanErr(errorOf(e))
    } finally {
      setCleaning(false)
    }
  }
  return (
    <div className="mx-auto max-w-4xl px-6 py-8 space-y-4">
      <h1 className="text-xl font-semibold tracking-tight">Settings</h1>
      <Card title="Host checks (zerosetup doctor)">
        {doctor.loading && !doctor.data ? <Spinner /> : doctor.error ? <ErrorState error={doctor.error} /> : (
          <ul className="space-y-2 text-[13px]">
            {doctor.data!.checks.map((c) => (
              <li key={c.name} className="flex items-start gap-3">
                {c.status === 'ok' ? <CheckCircle2 className="size-4 text-ok mt-0.5" aria-hidden /> : c.status === 'warn' ? <CircleAlert className="size-4 text-warn mt-0.5" aria-hidden /> : <XCircle className="size-4 text-bad mt-0.5" aria-hidden />}
                <div className="min-w-0"><p className="font-medium">{c.name}</p><p className="text-xs text-muted break-words">{c.detail}</p>{c.suggestion && <p className="text-xs text-fg/80">→ {c.suggestion}</p>}</div>
              </li>
            ))}
          </ul>
        )}
      </Card>
      <Card title="Execution defaults (read-only)">
        {config.data ? (
          <>
            <dl>{Object.entries(config.data.defaults).map(([k, v]) => <KV key={k} label={k}><Mono>{String(v)}</Mono></KV>)}</dl>
            <p className="text-xs text-muted mt-2">Per-run overrides (port, limits, env) are set when planning (<Mono>zerosetup plan --memory …</Mono>). Approval is always required and cannot be disabled.</p>
          </>
        ) : config.error ? <ErrorState error={config.error} /> : <Spinner />}
      </Card>
      <Card title="Storage & cache" actions={<Button size="sm" variant="danger" loading={cleaning} onClick={clean} icon={<Trash2 className="size-3.5" aria-hidden />}>Clean package caches</Button>}>
        {cleanErr && <div className="mb-3"><ErrorState error={cleanErr} compact /></div>}
        {config.data && (
          <dl>
            <KV label="Data directory"><Mono className="break-all">{config.data.home}</Mono></KV>
            <KV label="Database"><Mono className="break-all">{config.data.database}</Mono></KV>
          </dl>
        )}
        {cache.data && (
          <dl>
            <KV label="Source cache">{cache.data.sourceCheckouts} checkouts · {bytes(cache.data.sourceBytes)}</KV>
            <KV label="Cache volumes">{cache.data.volumes?.length ? cache.data.volumes.map((v) => <Mono key={v} className="mr-2">{v}</Mono>) : 'none'}</KV>
          </dl>
        )}
        <p className="text-xs text-muted mt-2">Cleanup only touches resources labelled <Mono>io.zerosetup.role=cache</Mono>. Nothing else on your Docker host is affected.</p>
      </Card>
      <Card title="Appearance">
        <div className="flex gap-2">
          {(['dark', 'light'] as const).map((t) => (
            <Button key={t} size="sm" variant={th === t ? 'primary' : 'secondary'} aria-pressed={th === t} onClick={() => { setTheme(t); setTh(t) }}
              icon={t === 'dark' ? <Moon className="size-3.5" aria-hidden /> : <Sun className="size-3.5" aria-hidden />}>{t === 'dark' ? 'Dark' : 'Light'}</Button>
          ))}
        </div>
      </Card>
      {config.data && <p className="text-2xs text-faint">zerosetup {config.data.version}</p>}
    </div>
  )
}
