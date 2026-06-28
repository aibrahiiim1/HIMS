// Data-driven, clickable summary cards for the inventory pages. Every value is computed
// from the live rows the page already fetched (never hardcoded), so a card count always
// equals the table count for that filter. Clicking a card with onClick applies/toggles the
// corresponding filter on the page. Honest labels only (unlinked_bmc, credential_required,
// unclassified, …); a metric that isn't supported is simply not rendered, never faked.

export interface SummaryCard {
  label: string
  value: number
  tone?: 'ok' | 'warn' | 'crit' | 'muted' | 'info'
  active?: boolean
  onClick?: () => void
  title?: string
}

const TONE: Record<string, string> = {
  ok: 'var(--ok)', warn: 'var(--warn)', crit: 'var(--crit)', info: 'var(--accent, #3b82f6)', muted: 'var(--text-muted)',
}

export function SummaryCards({ cards, loading, error }: { cards: SummaryCard[]; loading?: boolean; error?: string }) {
  if (loading) return <div className="loading" style={{ padding: 8 }}>Loading summary…</div>
  if (error) return <div className="error-msg" style={{ margin: '0 0 10px' }}>{error}</div>
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(140px, 1fr))', gap: 8, marginBottom: 14 }}>
      {cards.map((c) => {
        const color = TONE[c.tone ?? 'muted'] ?? TONE.muted
        const clickable = !!c.onClick
        return (
          <button
            key={c.label}
            type="button"
            onClick={c.onClick}
            disabled={!clickable}
            title={c.title ?? (clickable ? `Filter: ${c.label}` : c.label)}
            style={{
              textAlign: 'left', cursor: clickable ? 'pointer' : 'default', background: 'var(--surface)',
              border: `1px solid ${c.active ? color : 'var(--border)'}`, borderRadius: 8, padding: '10px 12px',
              display: 'flex', flexDirection: 'column', gap: 4, outline: c.active ? `1px solid ${color}` : 'none',
            }}
          >
            <span style={{ fontSize: 11, color, textTransform: 'uppercase', letterSpacing: 0.4 }}>{c.label}</span>
            <span style={{ fontSize: 22, fontWeight: 700, lineHeight: 1 }}>{c.value}</span>
          </button>
        )
      })}
    </div>
  )
}
