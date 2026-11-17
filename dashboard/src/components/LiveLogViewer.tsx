import { ArrowDownToLine, Pause, RefreshCw, Search } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { api, errorOf } from '../lib/api'
import type { ApiError, SessionEvent } from '../types/api'
import { logLines } from '../hooks/useSessionEvents'
import { Button, ErrorState, cx } from './ui'

/**
 * Shows step output streamed live from session events, and application
 * output fetched from the container engine. Nothing here is synthesized.
 */
export function LiveLogViewer({ sessionId, events, streams, appAvailable, live }: {
  sessionId: string
  events: SessionEvent[]
  streams: string[]
  appAvailable: boolean
  live: boolean
}) {
  const options = useMemo(() => {
    const s = ['all-steps', ...streams.filter((x) => x !== 'app')]
    if (appAvailable) s.push('app')
    return s
  }, [streams, appAvailable])
  const [stream, setStream] = useState(appAvailable ? 'app' : 'all-steps')
  const [filter, setFilter] = useState('')
  const [follow, setFollow] = useState(true)
  const [appText, setAppText] = useState('')
  const [appErr, setAppErr] = useState<ApiError>()
  const [loadingApp, setLoadingApp] = useState(false)
  const box = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (appAvailable) setStream((s) => (s === 'all-steps' ? 'app' : s))
  }, [appAvailable])

  const fetchApp = useMemo(() => async (signal?: AbortSignal) => {
    setLoadingApp(true)
    try {
      setAppText(await api.logs(sessionId, 'app', '2000', signal))
      setAppErr(undefined)
    } catch (e) {
      if ((e as Error).name !== 'AbortError') setAppErr(errorOf(e))
    } finally {
      setLoadingApp(false)
    }
  }, [sessionId])

  useEffect(() => {
    if (stream !== 'app') return
    const ctrl = new AbortController()
    void fetchApp(ctrl.signal)
    if (!live || !follow) return () => ctrl.abort()
    const t = setInterval(() => void fetchApp(ctrl.signal), 3000)
    return () => {
      clearInterval(t)
      ctrl.abort()
    }
  }, [stream, live, follow, fetchApp])

  const lines = useMemo(() => {
    let ls: string[]
    if (stream === 'app') ls = appText.split('\n')
    else ls = logLines(events).filter((l) => stream === 'all-steps' || l.stream === stream).map((l) => (stream === 'all-steps' ? `[${l.stream}] ${l.line}` : l.line))
    if (filter) {
      const f = filter.toLowerCase()
      ls = ls.filter((l) => l.toLowerCase().includes(f))
    }
    return ls.slice(-5000)
  }, [stream, appText, events, filter])

  useEffect(() => {
    if (follow && box.current) box.current.scrollTop = box.current.scrollHeight
  }, [lines, follow])

  return (
    <div className="flex flex-col h-full min-h-0">
      <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2">
        <div role="tablist" aria-label="Log stream" className="flex flex-wrap gap-1">
          {options.map((o) => (
            <button
              key={o}
              role="tab"
              aria-selected={stream === o}
              onClick={() => setStream(o)}
              className={cx('h-7 rounded-md px-2 font-mono text-[11px]', stream === o ? 'bg-raised text-fg' : 'text-muted hover:text-fg')}
            >
              {o === 'all-steps' ? 'build' : o}
            </button>
          ))}
        </div>
        <div className="ml-auto flex items-center gap-1.5">
          <label className="flex items-center gap-1.5 h-7 rounded-md border border-line px-2">
            <Search className="size-3.5 text-faint" aria-hidden />
            <span className="sr-only">Filter logs</span>
            <input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter" className="w-28 bg-transparent text-xs outline-none" />
          </label>
          <Button size="sm" variant="ghost" onClick={() => setFollow((f) => !f)} aria-pressed={follow}
            icon={follow ? <Pause className="size-3.5" aria-hidden /> : <ArrowDownToLine className="size-3.5" aria-hidden />}>
            {follow ? 'Pause' : 'Follow'}
          </Button>
          {stream === 'app' && (
            <Button size="sm" variant="ghost" onClick={() => void fetchApp()} loading={loadingApp} icon={<RefreshCw className="size-3.5" aria-hidden />}>Refresh</Button>
          )}
        </div>
      </div>
      {stream === 'app' && appErr ? (
        <div className="p-3"><ErrorState error={appErr} compact /></div>
      ) : (
        <div
          ref={box}
          role="log"
          aria-live={follow ? 'polite' : 'off'}
          aria-label="Logs"
          onScroll={(e) => {
            const el = e.currentTarget
            if (el.scrollHeight - el.scrollTop - el.clientHeight > 40 && follow) setFollow(false)
          }}
          className="flex-1 min-h-0 overflow-auto bg-bg/60 px-3 py-2 font-mono text-[11.5px] leading-[1.55]"
        >
          {lines.length === 0 || (lines.length === 1 && !lines[0]) ? (
            <p className="text-faint">{stream === 'app' ? 'No application output yet.' : 'No build output recorded for this stream.'}</p>
          ) : (
            lines.map((l, i) => (
              <div key={i} className={cx('whitespace-pre-wrap break-all', /error|ERR!|failed|exception/i.test(l) ? 'text-bad' : /warn/i.test(l) ? 'text-warn' : 'text-fg/85')}>
                {l || ' '}
              </div>
            ))
          )}
        </div>
      )}
    </div>
  )
}
