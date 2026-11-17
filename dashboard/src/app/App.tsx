import { History as HistoryIcon, LayoutGrid, Settings as SettingsIcon } from 'lucide-react'
import { Component, type ReactNode } from 'react'
import { useRoute, type Route } from './router'
import { CommandCenter } from '../features/CommandCenter'
import { Inspect } from '../features/Inspect'
import { SessionWorkspace } from '../features/SessionWorkspace'
import { History } from '../features/History'
import { Settings } from '../features/Settings'
import { EmptyState, cx } from '../components/ui'
import { token } from '../lib/api'

class ErrorBoundary extends Component<{ children: ReactNode }, { error?: Error }> {
  state: { error?: Error } = {}
  static getDerivedStateFromError(error: Error) {
    return { error }
  }
  render() {
    if (this.state.error) {
      return (
        <div className="p-8">
          <div role="alert" className="rounded-lg border border-bad/30 bg-bad-soft p-4 text-[13px]">
            <p className="font-medium">This view crashed.</p>
            <p className="font-mono text-xs text-muted mt-1">{this.state.error.message}</p>
            <button className="mt-3 text-accent text-xs" onClick={() => this.setState({ error: undefined })}>Try again</button>
          </div>
        </div>
      )
    }
    return this.props.children
  }
}

function NavLink({ to, active, icon, children }: { to: string; active: boolean; icon: ReactNode; children: ReactNode }) {
  return (
    <a href={to} aria-current={active ? 'page' : undefined}
      className={cx('flex items-center gap-2 h-8 px-2.5 rounded-md text-[13px]', active ? 'bg-raised text-fg' : 'text-muted hover:text-fg')}>
      {icon}
      <span className="hidden sm:inline">{children}</span>
    </a>
  )
}

function view(r: Route) {
  switch (r.name) {
    case 'inspect':
      return <Inspect key={r.source + (r.ref ?? '')} source={r.source} gitRef={r.ref} />
    case 'session':
      return <SessionWorkspace key={r.id} id={r.id} tab={r.tab} />
    case 'history':
      return <History />
    case 'settings':
      return <Settings />
    default:
      return <CommandCenter />
  }
}

export function App() {
  const route = useRoute()
  if (!token()) {
    return (
      <div className="h-full grid place-items-center">
        <EmptyState title="Access token required">
          Open the dashboard with the URL printed by <code className="font-mono">zerosetup dashboard</code>. The token protects your local engine from other websites.
        </EmptyState>
      </div>
    )
  }
  return (
    <div className="min-h-full flex flex-col">
      <a href="#main" className="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 bg-surface px-3 py-2 rounded">Skip to content</a>
      <header className="sticky top-0 z-20 border-b border-line bg-bg/85 backdrop-blur">
        <div className="mx-auto max-w-[1400px] flex items-center gap-4 px-6 h-12">
          <a href="#/" className="flex items-center gap-2 font-semibold tracking-tight">
            <svg viewBox="0 0 32 32" className="size-5" aria-hidden><rect width="32" height="32" rx="8" fill="var(--zs-raised)" /><path d="M9 10h14L9 22h14" stroke="var(--zs-accent)" strokeWidth="2.6" fill="none" strokeLinecap="round" strokeLinejoin="round" /></svg>
            ZEROSETUP
          </a>
          <nav aria-label="Primary" className="flex items-center gap-1 ml-2">
            <NavLink to="#/" active={route.name === 'home'} icon={<LayoutGrid className="size-3.5" aria-hidden />}>Command center</NavLink>
            <NavLink to="#/history" active={route.name === 'history'} icon={<HistoryIcon className="size-3.5" aria-hidden />}>History</NavLink>
            <NavLink to="#/settings" active={route.name === 'settings'} icon={<SettingsIcon className="size-3.5" aria-hidden />}>Settings</NavLink>
          </nav>
          <span className="ml-auto text-2xs text-faint hidden md:inline">local · 127.0.0.1</span>
        </div>
      </header>
      <main id="main" className="flex-1">
        <ErrorBoundary key={window.location.hash}>{view(route)}</ErrorBoundary>
      </main>
    </div>
  )
}
