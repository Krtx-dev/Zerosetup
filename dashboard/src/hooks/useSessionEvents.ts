import { useEffect, useState } from 'react'
import type { SessionEvent } from '../types/api'
import { subscribeEvents, type StreamStatus } from '../lib/events'

const MAX_EVENTS = 5000

/** Live, reconnecting event stream for a session. */
export function useSessionEvents(sessionId: string | undefined) {
  const [events, setEvents] = useState<SessionEvent[]>([])
  const [status, setStatus] = useState<StreamStatus>('connecting')
  useEffect(() => {
    if (!sessionId) return
    setEvents([])
    return subscribeEvents(
      sessionId,
      (evs) => setEvents((prev) => {
        const next = prev.concat(evs)
        return next.length > MAX_EVENTS ? next.slice(next.length - MAX_EVENTS) : next
      }),
      setStatus,
    )
  }, [sessionId])
  return { events, status }
}

/** Extracts log lines (with their stream id) from log events. */
export function logLines(events: SessionEvent[]): { stream: string; line: string; seq: number }[] {
  const out: { stream: string; line: string; seq: number }[] = []
  for (const e of events) {
    if (e.type !== 'log') continue
    const stream = String(e.data?.stream ?? e.stage ?? '')
    const lines = Array.isArray(e.data?.lines) ? (e.data.lines as unknown[]) : [e.message]
    for (const l of lines) out.push({ stream, line: String(l), seq: e.seq })
  }
  return out
}
