export const ACCENT       = '#818cf8';
export const ACCENT_GLOW  = 'rgba(129,140,248,0.25)';
export const ACCENT_BORDER = 'rgba(129,140,248,0.35)';

export type HealthStatus = 'healthy' | 'degraded' | 'unreachable' | 'unknown';

export const HEALTH_COLORS: Record<HealthStatus, { dot: string; bg: string; border: string; label: string }> = {
  healthy:     { dot: '#34d399', bg: 'rgba(16,185,129,0.12)',  border: 'rgba(16,185,129,0.28)',  label: 'Healthy' },
  degraded:    { dot: '#fbbf24', bg: 'rgba(251,191,36,0.12)',  border: 'rgba(251,191,36,0.28)',  label: 'Degraded' },
  unreachable: { dot: '#f87171', bg: 'rgba(220,38,38,0.12)',   border: 'rgba(220,38,38,0.25)',   label: 'Unreachable' },
  unknown:     { dot: '#64748b', bg: 'rgba(100,116,139,0.12)', border: 'rgba(100,116,139,0.22)', label: 'Not probed' },
};

export function timeAgo(iso: string | null): string {
  if (!iso) return '—';
  const diff = Date.now() - new Date(iso).getTime();
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

export function slugify(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
}

export const inputStyle: React.CSSProperties = {
  width: '100%', padding: '8px 11px', borderRadius: '8px', fontSize: '13px',
  background: 'var(--tm-inset-deep)', border: '1px solid var(--tm-divider)',
  color: 'var(--tm-card-text)', outline: 'none', boxSizing: 'border-box',
};

export const labelStyle: React.CSSProperties = {
  fontSize: '11px', fontWeight: 600, color: 'var(--tm-card-text-muted)',
  display: 'block', marginBottom: '5px',
};

export const sectionLabel: React.CSSProperties = {
  fontSize: '9px', fontWeight: 700, letterSpacing: '0.1em', textTransform: 'uppercase',
  color: 'rgba(255,255,255,0.2)', margin: '18px 0 10px 0',
};

export const nestedSurface: React.CSSProperties = {
  background: 'var(--tm-inset-deep)', border: '1px solid var(--tm-divider)',
  borderRadius: '10px', padding: '12px',
};
