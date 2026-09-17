import { C } from '../../../constants';

// ── Shared styles + SectionHeader for node property panels ──────────────────

export const fieldStyle: React.CSSProperties = {
  width: '100%', padding: '10px 12px', borderRadius: 8,
  borderWidth: '1px', borderStyle: 'solid', borderColor: 'rgba(255,255,255,0.12)',
  background: 'rgba(255,255,255,0.05)',
  color: C.text, fontSize: 14, outline: 'none', boxSizing: 'border-box',
};

export const sectionHdrStyle: React.CSSProperties = {
  fontSize: 11, fontWeight: 700, color: C.textMuted,
  textTransform: 'uppercase', letterSpacing: '0.06em',
};

export const chipStyle: React.CSSProperties = {
  fontSize: 12, padding: '4px 10px', borderRadius: 20,
  background: 'rgba(255,255,255,0.06)', color: C.textMuted,
  fontFamily: 'JetBrains Mono, monospace', display: 'inline-block',
};

export const selectStyle: React.CSSProperties = {
  ...fieldStyle, padding: '7px 10px', fontSize: 13, cursor: 'pointer',
};

export function isSectionOpen(openSections: Record<string, boolean>, id: string, def: boolean) {
  return openSections[id] ?? def;
}

export function SectionHeader({
  id, label, defaultOpen, openSections, setOpenSections,
}: {
  id: string;
  label: string;
  defaultOpen: boolean;
  openSections: Record<string, boolean>;
  setOpenSections: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
}) {
  const open = isSectionOpen(openSections, id, defaultOpen);
  return (
    <button
      onClick={() => setOpenSections(s => ({ ...s, [id]: !open }))}
      style={{ display: 'flex', alignItems: 'center', gap: 6, background: 'none', border: 'none', cursor: 'pointer', padding: '2px 0', width: '100%', textAlign: 'left' }}
    >
      <span style={{ ...sectionHdrStyle, flex: 1 }}>{label}</span>
      <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted, transform: open ? 'rotate(180deg)' : 'none', transition: 'transform 0.15s' }}>expand_more</span>
    </button>
  );
}
