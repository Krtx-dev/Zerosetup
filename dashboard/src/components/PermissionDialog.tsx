import { ShieldAlert } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import type { Plan } from '../types/api'
import { Button, Mono } from './ui'

/**
 * Informed-consent dialog. Approval is bound to the exact plan hash shown;
 * secrets are entered here and sent only with the approval request (never stored).
 */
export function PermissionDialog({ open, plan, planHash, repository, busy, onCancel, onApprove }: {
  open: boolean
  plan: Plan
  planHash: string
  repository: string
  busy?: boolean
  onCancel: () => void
  onApprove: (secrets: Record<string, string>) => void
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const secretNames = plan.env.filter((e) => e.secret && e.required).map((e) => e.name)
  const [secrets, setSecrets] = useState<Record<string, string>>({})
  const [ack, setAck] = useState(false)
  useEffect(() => {
    const d = ref.current
    if (!d) return
    if (open && !d.open) d.showModal()
    if (!open && d.open) d.close()
  }, [open])
  const missing = secretNames.filter((n) => !secrets[n])
  return (
    <dialog
      ref={ref}
      onCancel={(e) => {
        e.preventDefault()
        onCancel()
      }}
      aria-labelledby="perm-title"
      className="m-auto w-[min(560px,92vw)] rounded-xl border border-line-strong bg-surface p-0 text-fg backdrop:bg-black/60"
    >
      <form
        method="dialog"
        onSubmit={(e) => {
          e.preventDefault()
          if (ack && !missing.length) onApprove(secrets)
        }}
        className="p-5 space-y-4"
      >
        <div className="flex items-start gap-3">
          <ShieldAlert className="size-5 text-warn mt-0.5" aria-hidden />
          <div>
            <h2 id="perm-title" className="text-[15px] font-semibold">Run code from {repository}?</h2>
            <p className="text-[13px] text-muted mt-1">
              This executes the repository's code and its dependencies' install scripts inside a container.
            </p>
          </div>
        </div>
        <ul className="text-xs space-y-1.5 rounded-lg border border-line bg-bg/50 p-3">
          <li>✓ Non-root, all capabilities dropped, read-only root filesystem, no host mounts</li>
          <li>✓ CPU, memory and process limits; preview on 127.0.0.1 only</li>
          <li className="text-warn">! Outbound network is not filtered (installs and the app can reach the internet and your LAN)</li>
          <li className="text-warn">! Containers are not a strong security boundary against hostile code</li>
        </ul>
        <div className="text-xs space-y-1">
          <p className="text-muted">Commands that will run:</p>
          {plan.steps.map((s) => <p key={s.id}><Mono>{s.command.argv.join(' ')}</Mono></p>)}
          <p><Mono>{plan.start.argv.join(' ')}</Mono></p>
        </div>
        {secretNames.length > 0 && (
          <fieldset className="space-y-2">
            <legend className="text-xs text-muted mb-1">Secrets (sent once with this approval; never stored or logged)</legend>
            {secretNames.map((n) => (
              <label key={n} className="flex items-center gap-2">
                <span className="w-40 font-mono text-xs">{n}</span>
                <input type="password" autoComplete="off" value={secrets[n] ?? ''} onChange={(e) => setSecrets({ ...secrets, [n]: e.target.value })}
                  className="flex-1 h-8 rounded-md border border-line bg-bg px-2 text-xs outline-none focus:border-accent/60" />
              </label>
            ))}
          </fieldset>
        )}
        <label className="flex items-start gap-2 text-[13px]">
          <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} className="mt-0.5 accent-[var(--zs-accent)]" />
          <span>I understand and want to run plan <Mono className="text-accent">{planHash}</Mono></span>
        </label>
        <div className="flex justify-end gap-2 pt-1">
          <Button type="button" variant="ghost" onClick={onCancel}>Cancel</Button>
          <Button type="submit" variant="primary" disabled={!ack || missing.length > 0} loading={busy}>Approve and run</Button>
        </div>
      </form>
    </dialog>
  )
}
