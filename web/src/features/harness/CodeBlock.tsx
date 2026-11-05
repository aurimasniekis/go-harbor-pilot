import { useState } from 'react'
import { Check, Copy } from 'lucide-react'
import { Button } from '@/components/ui'
import h from './harness.module.css'

/** A copyable code block. */
export function CodeBlock({ code, label }: { code: string; label: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className={h.code}>
      <pre aria-label={label} tabIndex={0}>
        <code>{code}</code>
      </pre>
      <span className={h.copy}>
        <Button
          size="small"
          variant="ghost"
          icon={copied ? Check : Copy}
          aria-label={`Copy ${label}`}
          onClick={() => {
            void navigator.clipboard?.writeText(code).then(() => {
              setCopied(true)
              window.setTimeout(() => setCopied(false), 1500)
            })
          }}
        />
      </span>
    </div>
  )
}
