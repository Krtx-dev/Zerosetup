// Small, accessible UI primitives shared across the dashboard.
import { AlertTriangle, Inbox, Loader2, RefreshCw } from 'lucide-react'
import type { ButtonHTMLAttributes, ReactNode } from 'react'
import type { ApiError } from '../types/api'
import type { Tone } from '../lib/format'

export function cx(...c: (string | false | null | undefined)[]): string {
  return c.filter(Boolean).join(' ')
}

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger'

export function Button({
  variant = 'secondary',
  size = 'md',
  loading,
  icon,
  children,
  className,
  disabled,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: 'sm' | 'md'; loading?: boolean; icon?: ReactNode }) {
  const styles: Record<Variant, string> = {
    primary: 'bg-accent text-[#04131c] hover:brightness-110 font-semibold',
    secondary: 'bg-raised text-fg border border-line hover:border-line-strong',
    ghost: 'text-muted hover:text-fg hover:bg-raised',
    danger: 'bg-bad-soft text-bad border border-bad/30 hover:border-bad/60',
  }
  return (
    <button
      {...rest}
      disabled={disabled || loading}
      className={cx(
        'inline-flex items-center justify-center gap-2 rounded-lg transition-[background,border,filter,color] disabled:opacity-50 disabled:cursor-not-allowed whitespace-nowrap',
        size === 'sm' ? 'h-7 px-2.5 text-xs' : 'h-9 px-3.5 text-[13px]',
        styles[variant],
        className,
      )}
    >
      {loading ? <Loader2 className="size-3.5 animate-spin" aria-hidden /> : icon}
      {children}
    </button>
  )
}

export function Card({ title, actions, children, className, padded = true }: {
  title?: ReactNode
  actions?: ReactNode
  children: ReactNode
  className?: string
  padded?: boolean
}) {
  return (
    <section className={cx('rounded-[var(--radius-card)] border border-line bg-surface', className)}>
      {(title || actions) && (
        <header className="flex items-center justify-between gap-3 border-b border-line px-4 h-11">
          <h2 className="text-[13px] font-medium text-fg">{title}</h2>
          <div className="flex items-center gap-2">{actions}</div>
        </header>
      )}
      <div className={padded ? 'p-4' : undefined}>{children}</div>
    </section>
  )
}

const toneClass: Record<Tone, string> = {
  ok: 'bg-ok-soft text-ok',
  warn: 'bg-warn-soft text-warn',
  bad: 'bg-bad-soft text-bad',
  info: 'bg-accent-soft text-accent',
  busy: 'bg-accent-soft text-accent',
  muted: 'bg-raised text-muted',
}

export function Badge({ tone = 'muted', children, className }: { tone?: Tone; children: ReactNode; className?: string }) {
  return (
    <span className={cx('inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-2xs font-medium uppercase tracking-wide', toneClass[tone], className)}>
      {children}
    </span>
  )
}

export function Dot({ tone }: { tone: Tone }) {
  const c: Record<Tone, string> = { ok: 'bg-ok', warn: 'bg-warn', bad: 'bg-bad', info: 'bg-accent', busy: 'bg-accent', muted: 'bg-faint' }
  return (
    <span className="relative inline-flex size-2" aria-hidden>
      {tone === 'busy' && <span className={cx('absolute inset-0 rounded-full opacity-60 animate-ping', c[tone])} />}
      <span className={cx('relative inline-flex size-2 rounded-full', c[tone])} />
    </span>
  )
}

export function Spinner({ label }: { label?: string }) {
  return (
    <div role="status" className="flex items-center gap-2 text-muted text-[13px]">
      <Loader2 className="size-4 animate-spin" aria-hidden />
      {label ?? 'Loading…'}
    </div>
  )
}

export function EmptyState({ icon, title, children }: { icon?: ReactNode; title: string; children?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center text-center gap-2 py-10 px-6">
      <div className="text-faint">{icon ?? <Inbox className="size-6" aria-hidden />}</div>
      <p className="text-[13px] font-medium text-fg">{title}</p>
      {children && <div className="text-[13px] text-muted max-w-md">{children}</div>}
    </div>
  )
}

export function ErrorState({ error, onRetry, compact }: { error: ApiError; onRetry?: () => void; compact?: boolean }) {
  return (
    <div role="alert" className={cx('rounded-lg border border-bad/30 bg-bad-soft', compact ? 'p-3' : 'p-4')}>
      <div className="flex items-start gap-3">
        <AlertTriangle className="size-4 text-bad mt-0.5 shrink-0" aria-hidden />
        <div className="min-w-0 flex-1 space-y-1">
          <p className="text-[13px] text-fg font-medium">
            {error.summary} <span className="font-mono text-2xs text-bad ml-1">{error.code}</span>
          </p>
          {error.detail && <p className="text-xs text-muted whitespace-pre-wrap">{error.detail}</p>}
          {error.suggestion && <p className="text-xs text-fg/80">→ {error.suggestion}</p>}
        </div>
        {onRetry && (
          <Button size="sm" variant="ghost" onClick={onRetry} icon={<RefreshCw className="size-3.5" aria-hidden />}>
            Retry
          </Button>
        )}
      </div>
    </div>
  )
}

export function Mono({ children, className }: { children: ReactNode; className?: string }) {
  return <code className={cx('font-mono text-[12px] text-fg/90', className)}>{children}</code>
}

export function KV({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[120px_1fr] gap-3 py-1.5 text-[13px]">
      <dt className="text-muted">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  )
}
