import { C } from '../../constants';

export const labelStyle: React.CSSProperties = {
  fontSize: 12, color: 'var(--tm-card-text-subtle)', marginBottom: 4, display: 'block',
};

export const inputStyle: React.CSSProperties = {
  width: '100%', padding: '7px 10px', borderRadius: 6,
  border: `1px solid ${C.outlineVariant}`, background: C.surfaceLow,
  color: 'var(--tm-card-text)', fontSize: 13, boxSizing: 'border-box', outline: 'none',
};

export const fieldWrap: React.CSSProperties = { marginBottom: 14 };
