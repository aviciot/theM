'use client';
import { type MCPServer } from '@/lib/api';
import { type HealthStatus, ACCENT, ACCENT_GLOW, ACCENT_BORDER, nestedSurface, timeAgo } from './mcpConstants';
import { HealthBadge, TransportBadge, AuthBadge } from './MCPBadges';

export function MCPServerCard({
  server,
  selected,
  onClick,
}: {
  server: MCPServer;
  selected: boolean;
  onClick: () => void;
}) {
  const hs = (server.health_status as HealthStatus) || 'unknown';
  const chromaGrad = selected
    ? `linear-gradient(145deg, rgba(129,140,248,0.18) 0%, rgba(99,102,241,0.10) 40%, #0a0d14 100%)`
    : `linear-gradient(145deg, rgba(129,140,248,0.07) 0%, rgba(0,0,0,0) 50%, #0a0d14 100%)`;

  return (
    <article
      className="glass-card chroma-card"
      onClick={onClick}
      style={{
        padding: '20px', display: 'flex', flexDirection: 'column', gap: '12px',
        borderRadius: '20px', position: 'relative', cursor: 'pointer',
        '--card-border': selected ? ACCENT : ACCENT_BORDER,
        '--card-gradient': chromaGrad,
        boxShadow: selected ? `0 0 0 2px ${ACCENT}, 0 8px 32px rgba(0,0,0,0.4)` : undefined,
      } as React.CSSProperties}
    >
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: '10px' }}>
        <div style={{
          width: '48px', height: '48px', flexShrink: 0, borderRadius: '12px',
          background: `radial-gradient(circle at 30% 25%, ${ACCENT_GLOW}, transparent 65%),
                       linear-gradient(145deg, var(--tm-inset), var(--tm-inset-deep))`,
          border: `1px solid ${ACCENT_BORDER}`,
          boxShadow: `0 0 16px ${ACCENT_GLOW}`,
          display: 'flex', alignItems: 'center', justifyContent: 'center',
        }}>
          <span className="material-symbols-outlined" style={{ fontSize: '22px', color: ACCENT }}>electrical_services</span>
        </div>

        <div style={{ flex: 1, minWidth: 0, paddingTop: '2px' }}>
          <h3 style={{ fontSize: '15px', fontWeight: 700, color: 'var(--tm-card-text)', margin: '0 0 3px 0', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
            {server.name}
          </h3>
          <p style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', margin: 0, fontFamily: 'monospace', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
            {server.slug}
          </p>
        </div>

        <HealthBadge status={hs} />
      </div>

      <p style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', lineHeight: 1.5, margin: 0, display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden', minHeight: '36px' }}>
        {server.description || <span style={{ opacity: 0.35 }}>No description</span>}
      </p>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '8px' }}>
        <div style={{ ...nestedSurface, padding: '8px 10px', display: 'flex', alignItems: 'center', gap: '7px' }}>
          <span className="material-symbols-outlined" style={{ fontSize: '14px', color: 'var(--tm-card-text-muted)' }}>build</span>
          <div>
            <p style={{ fontSize: '14px', fontWeight: 700, color: 'var(--tm-card-text)', margin: 0, lineHeight: 1 }}>
              {server.tools_count ?? server.tools_manifest?.length ?? 0}
            </p>
            <p style={{ fontSize: '9px', color: 'var(--tm-card-text-muted)', textTransform: 'uppercase', fontWeight: 700, letterSpacing: '0.08em', margin: '2px 0 0 0' }}>tools</p>
          </div>
        </div>
        <div style={{ ...nestedSurface, padding: '8px 10px', display: 'flex', alignItems: 'center', gap: '7px' }}>
          <span className="material-symbols-outlined" style={{ fontSize: '14px', color: 'var(--tm-card-text-muted)' }}>schedule</span>
          <div>
            <p style={{ fontSize: '12px', fontWeight: 700, color: 'var(--tm-card-text)', margin: 0, lineHeight: 1, whiteSpace: 'nowrap' }}>
              {timeAgo(server.last_checked_at)}
            </p>
            <p style={{ fontSize: '9px', color: 'var(--tm-card-text-muted)', textTransform: 'uppercase', fontWeight: 700, letterSpacing: '0.08em', margin: '2px 0 0 0' }}>checked</p>
          </div>
        </div>
      </div>

      <div style={{ display: 'flex', gap: '6px', flexWrap: 'wrap', alignItems: 'center' }}>
        <TransportBadge t={server.transport} />
        <AuthBadge a={server.auth_type} />
        {!server.enabled && (
          <span style={{ fontSize: '9px', fontWeight: 700, letterSpacing: '0.07em', textTransform: 'uppercase', padding: '2px 6px', borderRadius: '6px', background: 'rgba(100,116,139,0.12)', border: '1px solid rgba(100,116,139,0.22)', color: '#64748b' }}>Disabled</span>
        )}
      </div>
    </article>
  );
}
