import { AlertTriangle, Clock3, LineChart, OctagonAlert } from 'lucide-react'
import { Badge, Button, QuotaBar } from '@/components/ui'
import type { BucketView } from '@/lib/api/types'
import { fmtDateTime, fmtRelative, fmtWindow } from '@/lib/format'
import { bucketPercent, bucketValueText } from './quota'
import q from './quota.module.css'

/** One bucket with its remaining bar and state flags. */
export function BucketRow({ b, onDetails }: { b: BucketView; onDetails?: () => void }) {
  const window = b.window_seconds ? fmtWindow(b.window_seconds, b.kind) : b.kind === 'balance' || b.kind === 'budget' ? fmtWindow(null, b.kind) : null
  return (
    <div className={q.bucket}>
      <QuotaBar
        label={
          <span>
            {b.label}
            {b.model && !b.label.toLowerCase().includes(b.model.toLowerCase()) && <span className="muted"> · {b.model}</span>}
          </span>
        }
        srLabel={`${b.label} remaining`}
        percent={bucketPercent(b)}
        stale={b.stale}
      />
      <div className={q.bucketMeta}>
        {/* The bar already shows the percentage; repeat only real amounts. */}
        {b.unit !== 'percent' && <span>{bucketValueText(b)}</span>}
        {window && <span>{b.unit !== 'percent' ? '· ' : ''}{window}</span>}
        {b.reset_at && !b.reset_passed && <span>· resets {fmtRelative(b.reset_at)}</span>}
        {b.exhausted && <Badge tone="error" icon={OctagonAlert}>Exhausted</Badge>}
        {b.stale && <Badge tone="warning" icon={Clock3} title={`Observed ${fmtDateTime(b.observed_at)}`}>Stale</Badge>}
        {b.reset_passed && <Badge tone="info" title="The reported reset time has passed; these values describe an earlier window.">Reset passed</Badge>}
        {!b.official && <Badge tone="warning" icon={AlertTriangle}>Unofficial source</Badge>}
        {onDetails && (
          <Button size="small" variant="ghost" icon={LineChart} onClick={onDetails} aria-label={`History and forecast for ${b.label}`}>
            Forecast
          </Button>
        )}
      </div>
    </div>
  )
}
