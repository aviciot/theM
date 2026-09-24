'use client';

export function Toggle({ value, onChange }: { value: boolean; onChange: (v: boolean) => void }) {
  return (
    <button
      type="button"
      onClick={() => onChange(!value)}
      style={{
        display: 'flex', alignItems: 'center', gap: '10px',
        padding: '8px 16px', borderRadius: '9px', border: 'none',
        cursor: 'pointer', fontSize: '14px', fontWeight: 600,
        background: value ? 'rgba(16,185,129,0.15)' : 'rgba(100,116,139,0.15)',
        color: value ? '#34d399' : 'var(--tm-card-text-muted)',
        transition: 'all 0.18s',
      }}
    >
      <span style={{
        width: '32px', height: '18px', borderRadius: '9px', flexShrink: 0,
        background: value ? '#34d399' : '#475569',
        position: 'relative', display: 'inline-block',
        transition: 'background 0.18s',
      }}>
        <span style={{ position: 'absolute', top: '3px', left: value ? '17px' : '3px', width: '12px', height: '12px', borderRadius: '50%', background: '#fff', transition: 'left 0.18s' }} />
      </span>
      {value ? 'Enabled' : 'Disabled'}
    </button>
  );
}
