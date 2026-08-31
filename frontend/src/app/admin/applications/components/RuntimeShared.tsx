'use client';
import { useState } from 'react';
import { C, glass } from '../constants';

export function Section({ title, subtitle, icon, children, defaultOpen = true, accent }: {
  title: string; subtitle?: string; icon: string; children: React.ReactNode;
  defaultOpen?: boolean; accent?: string;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div style={{ ...glass, borderRadius: 12, marginBottom: 16, overflow: 'hidden' }}>
      <button
        onClick={() => setOpen(o => !o)}
        style={{ width: '100%', display: 'flex', alignItems: 'center', gap: 12, padding: '14px 20px', background: 'none', border: 'none', cursor: 'pointer', borderBottom: open ? '1px solid rgba(255,255,255,0.06)' : 'none' }}
      >
        <span className="material-symbols-outlined" style={{ fontSize: 18, color: accent ?? C.purple, flexShrink: 0 }}>{icon}</span>
        <div style={{ flex: 1, textAlign: 'left' }}>
          <div style={{ fontSize: 13, fontWeight: 700, color: C.text }}>{title}</div>
          {subtitle && <div style={{ fontSize: 11, color: C.textMuted, marginTop: 2 }}>{subtitle}</div>}
        </div>
        <span className="material-symbols-outlined" style={{ fontSize: 18, color: C.textMuted, transition: 'transform 0.15s', transform: open ? 'rotate(180deg)' : 'none' }}>expand_more</span>
      </button>
      {open && <div style={{ padding: '16px 20px', display: 'flex', flexDirection: 'column', gap: 14 }}>{children}</div>}
    </div>
  );
}

export function AgentSection({ slug, children, defaultOpen = true }: { slug: string; children: React.ReactNode; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div style={{ borderRadius: 10, border: '1px solid rgba(208,188,255,0.18)', marginBottom: 12, overflow: 'hidden', background: 'rgba(208,188,255,0.03)' }}>
      <button
        onClick={() => setOpen(o => !o)}
        style={{ width: '100%', display: 'flex', alignItems: 'center', gap: 10, padding: '10px 16px', background: 'none', border: 'none', cursor: 'pointer', borderBottom: open ? '1px solid rgba(208,188,255,0.1)' : 'none' }}
      >
        <span className="material-symbols-outlined" style={{ fontSize: 15, color: C.purple }}>smart_toy</span>
        <span style={{ flex: 1, textAlign: 'left', fontSize: 12, fontWeight: 700, color: C.text, fontFamily: 'JetBrains Mono, monospace' }}>{slug}</span>
        <span className="material-symbols-outlined" style={{ fontSize: 16, color: C.textMuted, transition: 'transform 0.15s', transform: open ? 'rotate(180deg)' : 'none' }}>expand_more</span>
      </button>
      {open && <div style={{ padding: '14px 16px', display: 'flex', flexDirection: 'column', gap: 12 }}>{children}</div>}
    </div>
  );
}

export function AgentSubLabel({ icon, label }: { icon: string; label: string }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 8 }}>
      <span className="material-symbols-outlined" style={{ fontSize: 13, color: C.textMuted }}>{icon}</span>
      <span style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.07em' }}>{label}</span>
    </div>
  );
}

export type SaveBtn = (onClick: () => void, busy: boolean, disabled: boolean, label?: string) => React.ReactNode;

export const sharedField: React.CSSProperties = {
  width: '100%', padding: '9px 12px', borderRadius: 7,
  border: '1px solid rgba(255,255,255,0.1)', background: 'rgba(255,255,255,0.05)',
  color: C.text, fontSize: 13, outline: 'none', boxSizing: 'border-box',
};

export const sharedLbl: React.CSSProperties = {
  fontSize: 11, fontWeight: 600, color: C.textMuted, letterSpacing: '0.06em',
  textTransform: 'uppercase', marginBottom: 5, display: 'block',
};

export function makeSaveBtn(field = sharedField): SaveBtn {
  return (onClick, busy, disabled, label = 'Save') => (
    <button onClick={onClick} disabled={busy || disabled} style={{ padding: '8px 14px', borderRadius: 7, border: `1px solid rgba(167,139,250,0.4)`, background: 'rgba(208,188,255,0.07)', color: C.purple, cursor: 'pointer', fontSize: 12, fontWeight: 600, whiteSpace: 'nowrap', flexShrink: 0, opacity: busy || disabled ? 0.45 : 1 }}>
      {busy ? '…' : label}
    </button>
  );
  void field;
}

export function badge(color: string, bg: string, border: string, text: string) {
  return <span style={{ fontSize: 10, fontWeight: 700, padding: '2px 7px', borderRadius: 20, background: bg, color, border: `1px solid ${border}` }}>{text}</span>;
}

export function ToggleBtn({ on, onToggle, colorOn = '#4ade80', title }: { on: boolean; onToggle: () => void; colorOn?: string; title?: string }) {
  return (
    <button
      onClick={onToggle}
      title={title}
      style={{ background: 'none', border: 'none', cursor: 'pointer', color: on ? colorOn : C.textMuted, display: 'flex', alignItems: 'center', padding: '3px 4px', borderRadius: 4, flexShrink: 0 }}
    >
      <span className="material-symbols-outlined" style={{ fontSize: 22 }}>{on ? 'toggle_on' : 'toggle_off'}</span>
    </button>
  );
}
