import type { SessionEvent } from '../types/api'
import { token } from './api'

export type StreamStatus = 'connecting' | 'open' | 'reconnecting' | 'closed'

/** Parses a Server-Sent Events byte stream into events. */
export function parseSSE(buffer: string): { events: SessionEvent[]; rest: string } {
  const events: SessionEvent[] = []
  const blocks = buffer.split('\n\n')
  const rest = blocks.pop() ?? ''
  for (const block of blocks) {
    let data = ''
    for (const line of block.split('\n')) {
      if (line.startsWith('data: ')) data += line.slice(6)
    }
    if (!data) continue
    try {
      events.push(JSON.parse(data) as SessionEvent)
    } catch {
      /* skip malformed frames */
    }
  }
  return { events, rest }
}

/**
 * Subscribes to a session's event stream using fetch streaming (so the
 * bearer token can be sent as a header). Reconnects with bounded backoff and
 * resumes from the last received sequence number.
 */
export function subscribeEvents(
  sessionId: string,
  onEvents: (events: SessionEvent[]) => void,
  onStatus: (s: StreamStatus) => void,
): () => void {
  const ctrl = new AbortController()
  let after = 0
  let attempt = 0
  let stopped = false

  const run = async () => {
    while (!stopped) {
      onStatus(attempt === 0 ? 'connecting' : 'reconnecting')
      try {
        const res = await fetch(`/api/v1/sessions/${encodeURIComponent(sessionId)}/events?after=${after}`, {
          headers: { Authorization: `Bearer ${token() ?? ''}` },
          signal: ctrl.signal,
        })
        if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`)
        onStatus('open')
        attempt = 0
        const reader = res.body.getReader()
        const dec = new TextDecoder()
        let buf = ''
        for (;;) {
          const { done, value } = await reader.read()
          if (done) break
          buf += dec.decode(value, { stream: true })
          const { events, rest } = parseSSE(buf)
          buf = rest
          if (events.length) {
            after = events[events.length - 1]!.seq
            onEvents(events)
          }
        }
      } catch (e) {
        if (stopped || (e as Error).name === 'AbortError') break
      }
      if (stopped) break
      attempt++
      const delay = Math.min(250 * 2 ** attempt, 5000)
      await new Promise((r) => setTimeout(r, delay))
    }
    onStatus('closed')
  }
  void run()
  return () => {
    stopped = true
    ctrl.abort()
  }
}
