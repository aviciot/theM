'use client';
import { type HealthStatus, HEALTH_COLORS } from './mcpConstants';

export function HealthBadge({ status, pulse }: { status: HealthStatus; pulse?: boolean }) {
  const c = HEALTH_COLORS[status] ?? HEALTH_COLORS.unknown;
  return (
    <span style={{
      fontSize: '9px', fontWeight: 700, letterSpacing: '0.07em', textTransform: 'uppercase',
      padding: '2px 8px', borderRadius: '9999px', display: 'inline-flex', alignItems: 'center', gap: '5px',
      background: c.bg, border: `1px solid ${c.border}`, color: c.dot,
    }}>
      <span style={{
        width: '5px', height: '5px', borderRadius: '50%', background: c.dot, display: 'inline-block',
        boxShadow: `0 0 6px ${c.dot}`,
        ...(pulse ? { animation: 'pulse 1.6s ease-in-out infinite' } : {}),
      }} />
      {c.label}
    </span>
  );
}

export function TransportBadge({ t }: { t: string | undefined }) {
  if (!t) return null;
  const color = t === 'streamable-http' ? '#818cf8' : t === 'http' ? '#38bdf8' : t === 'sse' ? '#a78bfa' : '#94a3b8';
  return (
    <span style={{
      fontSize: '9px', fontWeight: 700, letterSpacing: '0.06em', textTransform: 'uppercase',
      padding: '2px 6px', borderRadius: '6px',
      background: `${color}18`, border: `1px solid ${color}40`, color,
    }}>{t}</span>
  );
}

export function AuthBadge({ a }: { a: string }) {
  const color = a === 'none' ? '#64748b' : a === 'bearer' ? '#34d399' : a === 'header' ? '#fbbf24' : '#a78bfa';
  return (
    <span style={{
      fontSize: '9px', fontWeight: 700, letterSpacing: '0.06em', textTransform: 'uppercase',
      padding: '2px 6px', borderRadius: '6px',
      background: `${color}18`, border: `1px solid ${color}40`, color,
    }}>{a}</span>
  );
}
