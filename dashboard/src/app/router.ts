import { useEffect, useState } from 'react'

export type Route =
  | { name: 'home' }
  | { name: 'inspect'; source: string; ref?: string }
  | { name: 'session'; id: string; tab: string }
  | { name: 'history' }
  | { name: 'settings' }

export function parseRoute(hash: string): Route {
  const h = hash.replace(/^#/, '') || '/'
  const [path = '/', query = ''] = h.split('?')
  const q = new URLSearchParams(query)
  const parts = path.split('/').filter(Boolean)
  if (parts[0] === 'inspect' && q.get('source')) return { name: 'inspect', source: q.get('source')!, ref: q.get('ref') ?? undefined }
  if (parts[0] === 'sessions' && parts[1] && /^zs[0-9a-f]{8}$/.test(parts[1])) return { name: 'session', id: parts[1], tab: parts[2] ?? 'overview' }
  if (parts[0] === 'history') return { name: 'history' }
  if (parts[0] === 'settings') return { name: 'settings' }
  return { name: 'home' }
}

export function href(r: Route): string {
  switch (r.name) {
    case 'inspect': {
      const q = new URLSearchParams({ source: r.source })
      if (r.ref) q.set('ref', r.ref)
      return `#/inspect?${q.toString()}`
    }
    case 'session':
      return `#/sessions/${r.id}${r.tab && r.tab !== 'overview' ? `/${r.tab}` : ''}`
    case 'history':
      return '#/history'
    case 'settings':
      return '#/settings'
    default:
      return '#/'
  }
}

export function navigate(r: Route) {
  window.location.hash = href(r)
}

export function useRoute(): Route {
  const [route, setRoute] = useState(() => parseRoute(window.location.hash))
  useEffect(() => {
    const on = () => setRoute(parseRoute(window.location.hash))
    window.addEventListener('hashchange', on)
    return () => window.removeEventListener('hashchange', on)
  }, [])
  return route
}
