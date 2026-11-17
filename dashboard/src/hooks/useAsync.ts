import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { ApiError } from '../types/api'
import { errorOf } from '../lib/api'

export interface AsyncState<T> {
  data: T | undefined
  error: ApiError | undefined
  loading: boolean
  reload: () => void
}

/**
 * Runs an abortable async loader, re-running when deps change and
 * optionally polling. Stale responses are discarded via AbortController.
 */
export function useAsync<T>(fn: (signal: AbortSignal) => Promise<T>, deps: unknown[], pollMs?: number): AsyncState<T> {
  const [data, setData] = useState<T>()
  const [error, setError] = useState<ApiError>()
  const [loading, setLoading] = useState(true)
  const [tick, setTick] = useState(0)
  const fnRef = useRef(fn)
  useLayoutEffect(() => {
    fnRef.current = fn
  })

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    fnRef
      .current(ctrl.signal)
      .then((d) => {
        if (!ctrl.signal.aborted) {
          setData(d)
          setError(undefined)
        }
      })
      .catch((e: unknown) => {
        if (!ctrl.signal.aborted && (e as Error).name !== 'AbortError') setError(errorOf(e))
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick])

  useEffect(() => {
    if (!pollMs) return
    const t = setInterval(() => {
      if (document.visibilityState === 'visible') setTick((n) => n + 1)
    }, pollMs)
    return () => clearInterval(t)
  }, [pollMs])

  const reload = useCallback(() => setTick((n) => n + 1), [])
  return { data, error, loading, reload }
}
