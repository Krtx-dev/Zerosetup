import { ArrowRight, FolderGit2, GitBranch } from 'lucide-react'
import { useId, useState, type FormEvent } from 'react'
import { Button, cx } from './ui'

/** Client-side mirror of the server's URL rules, for instant feedback only.
 *  The server re-validates everything. */
export function validateSource(v: string): string | null {
  const s = v.trim()
  if (!s) return 'Enter a GitHub repository URL or a local directory path.'
  if (s.startsWith('git@') || s.startsWith('ssh://')) return 'SSH URLs are not supported. Use https://github.com/<owner>/<repo>.'
  if (s.startsWith('http://')) return 'Only https:// URLs are supported.'
  if (s.includes('://') || s.startsWith('github.com/')) {
    let u: URL
    try {
      u = new URL(s.includes('://') ? s : `https://${s}`)
    } catch {
      return 'Not a valid URL.'
    }
    if (u.username || u.password) return 'URLs containing credentials are refused.'
    if (!['github.com', 'www.github.com'].includes(u.hostname) || u.port) return 'Only github.com repositories are supported in this version.'
    if (u.pathname.split('/').filter(Boolean).length < 2) return 'URL must name a repository: https://github.com/<owner>/<repo>'
    return null
  }
  if (!s.startsWith('/') && !s.startsWith('~') && !s.startsWith('.')) return 'Local paths must be absolute (e.g. /Users/me/project).'
  return null
}

export function RepositoryInput({ onSubmit, busy, initial = '' }: { onSubmit: (source: string, ref?: string) => void; busy?: boolean; initial?: string }) {
  const [value, setValue] = useState(initial)
  const [ref, setRef] = useState('')
  const [showRef, setShowRef] = useState(false)
  const [touched, setTouched] = useState(false)
  const err = validateSource(value)
  const errId = useId()
  const submit = (e: FormEvent) => {
    e.preventDefault()
    setTouched(true)
    if (!err) onSubmit(value.trim(), ref.trim() || undefined)
  }
  return (
    <form onSubmit={submit} className="w-full" noValidate>
      <div
        className={cx(
          'flex items-center gap-2 rounded-xl border bg-surface pl-4 pr-1.5 h-14 transition-colors focus-within:border-accent/60',
          touched && err ? 'border-bad/50' : 'border-line-strong',
        )}
      >
        <FolderGit2 className="size-4 text-muted shrink-0" aria-hidden />
        <label htmlFor="repo-input" className="sr-only">Repository URL or path</label>
        <input
          id="repo-input"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onBlur={() => setTouched(true)}
          placeholder="Paste a GitHub repository URL…"
          autoComplete="off"
          spellCheck={false}
          aria-invalid={touched && !!err}
          aria-describedby={touched && err ? errId : undefined}
          className="flex-1 bg-transparent outline-none text-[15px] placeholder:text-faint font-mono"
        />
        <Button type="button" variant="ghost" size="sm" onClick={() => setShowRef((v) => !v)} aria-expanded={showRef} icon={<GitBranch className="size-3.5" aria-hidden />}>
          {ref || 'Ref'}
        </Button>
        <Button type="submit" variant="primary" loading={busy} icon={!busy && <ArrowRight className="size-4" aria-hidden />} className="h-11 px-4">
          Inspect repository
        </Button>
      </div>
      {showRef && (
        <div className="mt-2 flex items-center gap-2">
          <label htmlFor="ref-input" className="text-xs text-muted">Branch, tag or commit</label>
          <input
            id="ref-input"
            value={ref}
            onChange={(e) => setRef(e.target.value)}
            placeholder="main"
            className="h-8 rounded-md border border-line bg-surface px-2 font-mono text-xs outline-none focus:border-accent/60"
          />
        </div>
      )}
      {touched && err && (
        <p id={errId} className="mt-2 text-xs text-bad" role="alert">{err}</p>
      )}
      <p className="mt-2 text-xs text-faint">Inspection reads files only. Nothing from the repository runs until you approve a specific plan.</p>
    </form>
  )
}
