'use client';
import { useState } from 'react';
import { themApi, type LLMProviderKeyOut } from '@/lib/api';

interface RowTestState {
  loading: boolean;
  ok?: boolean;
  error?: string;
}

const btnStyle: React.CSSProperties = {
  padding: '5px 10px', borderRadius: '6px', border: '1px solid var(--tm-border)',
  background: 'transparent', color: 'var(--tm-text-muted)', cursor: 'pointer',
  fontSize: '12px', fontWeight: 600,
};

export function LLMProviderKeysPanel({
  providerName,
  keys,
  onChanged,
}: {
  providerName: string;
  keys: LLMProviderKeyOut[];
  onChanged: () => void;
}) {
  // Always the caller's own tenant-owned keys — for the bootstrap tenant,
  // this is the-M's own platform keys, same code path as any other tenant.
  const api = {
    create: themApi.createProviderKey,
    update: themApi.updateProviderKey,
    del: themApi.deleteProviderNamedKey,
    setDefault: themApi.setDefaultProviderKey,
    test: themApi.testProviderKey,
  };

  const [newName, setNewName] = useState('');
  const [newKey, setNewKey] = useState('');
  const [creating, setCreating] = useState(false);
  const [createErr, setCreateErr] = useState<string | null>(null);

  const [renaming, setRenaming] = useState<Record<number, string>>({});
  const [rotating, setRotating] = useState<Record<number, string>>({});
  const [busy, setBusy] = useState<Record<number, boolean>>({});
  const [rowMsg, setRowMsg] = useState<Record<number, { ok: boolean; text: string } | null>>({});
  const [testState, setTestState] = useState<Record<number, RowTestState>>({});

  async function handleCreate() {
    if (!newName.trim() || !newKey.trim()) return;
    setCreating(true);
    setCreateErr(null);
    try {
      await api.create(providerName, { name: newName.trim(), api_key: newKey.trim() });
      setNewName('');
      setNewKey('');
      onChanged();
    } catch (e: unknown) {
      setCreateErr(e instanceof Error ? e.message : 'Failed to add key');
    } finally {
      setCreating(false);
    }
  }

  async function handleRename(keyId: number) {
    const name = (renaming[keyId] ?? '').trim();
    if (!name) return;
    setBusy((b) => ({ ...b, [keyId]: true }));
    try {
      await api.update(providerName, keyId, { name });
      setRenaming((r) => ({ ...r, [keyId]: '' }));
      setRowMsg((m) => ({ ...m, [keyId]: { ok: true, text: 'Renamed' } }));
      onChanged();
    } catch (e: unknown) {
      setRowMsg((m) => ({ ...m, [keyId]: { ok: false, text: e instanceof Error ? e.message : 'Rename failed' } }));
    } finally {
      setBusy((b) => ({ ...b, [keyId]: false }));
    }
  }

  async function handleRotate(keyId: number) {
    const apiKey = (rotating[keyId] ?? '').trim();
    if (!apiKey) return;
    setBusy((b) => ({ ...b, [keyId]: true }));
    try {
      await api.update(providerName, keyId, { api_key: apiKey });
      setRotating((r) => ({ ...r, [keyId]: '' }));
      setRowMsg((m) => ({ ...m, [keyId]: { ok: true, text: 'Rotated' } }));
      onChanged();
    } catch (e: unknown) {
      setRowMsg((m) => ({ ...m, [keyId]: { ok: false, text: e instanceof Error ? e.message : 'Rotate failed' } }));
    } finally {
      setBusy((b) => ({ ...b, [keyId]: false }));
    }
  }

  async function handleDelete(keyId: number) {
    setBusy((b) => ({ ...b, [keyId]: true }));
    try {
      await api.del(providerName, keyId);
      onChanged();
    } catch (e: unknown) {
      setRowMsg((m) => ({ ...m, [keyId]: { ok: false, text: e instanceof Error ? e.message : 'Delete failed' } }));
      setBusy((b) => ({ ...b, [keyId]: false }));
    }
  }

  async function handleSetDefault(keyId: number) {
    setBusy((b) => ({ ...b, [keyId]: true }));
    try {
      await api.setDefault(providerName, keyId);
      onChanged();
    } catch (e: unknown) {
      setRowMsg((m) => ({ ...m, [keyId]: { ok: false, text: e instanceof Error ? e.message : 'Failed to set default' } }));
    } finally {
      setBusy((b) => ({ ...b, [keyId]: false }));
    }
  }

  async function handleTest(keyId: number) {
    setTestState((t) => ({ ...t, [keyId]: { loading: true } }));
    try {
      const res = await api.test(providerName, keyId);
      setTestState((t) => ({ ...t, [keyId]: { loading: false, ok: res.ok, error: res.error } }));
      onChanged();
    } catch (e: unknown) {
      setTestState((t) => ({ ...t, [keyId]: { loading: false, ok: false, error: e instanceof Error ? e.message : 'Test failed' } }));
    }
  }

  return (
    <div style={{ marginTop: '14px', paddingTop: '14px', borderTop: '1px solid rgba(132,157,188,.1)' }}>
      <div style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: '10px' }}>
        Named keys
      </div>

      {keys.length === 0 && (
        <div style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', marginBottom: '10px' }}>
          No keys saved yet.
        </div>
      )}

      {keys.map((k) => {
        const ts = testState[k.id];
        return (
          <div key={k.id} style={{ padding: '10px 0', borderBottom: '1px solid rgba(132,157,188,.06)' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '10px', flexWrap: 'wrap' }}>
              <span style={{ fontSize: '13px', fontWeight: 600, color: '#fff' }}>{k.name}</span>
              {k.is_default && (
                <span style={{ fontSize: '11px', padding: '2px 8px', borderRadius: '20px', background: 'rgba(0,209,255,0.12)', color: '#00d1ff' }}>default</span>
              )}
              <span style={{ fontSize: '12px', color: 'var(--tm-card-text-muted)', fontFamily: 'JetBrains Mono, monospace' }}>{k.masked}</span>
              {k.last_test_ok !== null && k.last_test_ok !== undefined && (
                <span style={{ fontSize: '11px', color: k.last_test_ok ? '#4ac088' : '#e05252' }}>
                  {k.last_test_ok ? 'last test OK' : 'last test failed'}
                </span>
              )}

              <div style={{ flex: 1 }} />

              {!k.is_default && (
                <button style={btnStyle} disabled={!!busy[k.id]} onClick={() => handleSetDefault(k.id)}>
                  Set default
                </button>
              )}
              <button style={btnStyle} disabled={ts?.loading} onClick={() => handleTest(k.id)}>
                {ts?.loading ? 'Testing…' : 'Test'}
              </button>
              <button style={{ ...btnStyle, color: '#e05252', borderColor: 'rgba(224,82,82,0.3)' }} disabled={!!busy[k.id]} onClick={() => handleDelete(k.id)}>
                Delete
              </button>
            </div>

            {ts && !ts.loading && ts.ok !== undefined && (
              <p style={{ margin: '6px 0 0', fontSize: '12px', color: ts.ok ? '#4ac088' : '#e05252' }}>
                {ts.ok ? 'Connection OK' : (ts.error ?? 'Connection failed')}
              </p>
            )}

            <div style={{ display: 'flex', gap: '8px', marginTop: '8px', flexWrap: 'wrap' }}>
              <input
                type="text"
                placeholder="Rename to…"
                value={renaming[k.id] ?? ''}
                onChange={(e) => setRenaming((r) => ({ ...r, [k.id]: e.target.value }))}
                style={{ flex: '1 1 160px', background: 'var(--tm-input-bg, rgba(0,0,0,0.3))', border: '1px solid rgba(132,157,188,0.2)', borderRadius: '6px', padding: '6px 10px', color: '#fff', fontSize: '12px' }}
              />
              <button style={btnStyle} disabled={!!busy[k.id] || !(renaming[k.id] ?? '').trim()} onClick={() => handleRename(k.id)}>
                Rename
              </button>
              <input
                type="password"
                placeholder="Rotate secret to…"
                value={rotating[k.id] ?? ''}
                onChange={(e) => setRotating((r) => ({ ...r, [k.id]: e.target.value }))}
                style={{ flex: '1 1 160px', background: 'var(--tm-input-bg, rgba(0,0,0,0.3))', border: '1px solid rgba(132,157,188,0.2)', borderRadius: '6px', padding: '6px 10px', color: '#fff', fontSize: '12px' }}
              />
              <button style={btnStyle} disabled={!!busy[k.id] || !(rotating[k.id] ?? '').trim()} onClick={() => handleRotate(k.id)}>
                Rotate
              </button>
            </div>

            {rowMsg[k.id] && (
              <p style={{ margin: '6px 0 0', fontSize: '12px', color: rowMsg[k.id]!.ok ? '#4ac088' : '#e05252' }}>
                {rowMsg[k.id]!.text}
              </p>
            )}
          </div>
        );
      })}

      <div style={{ display: 'flex', gap: '8px', marginTop: '12px' }}>
        <input
          type="text"
          placeholder="Key name (e.g. Key_for_april)"
          value={newName}
          onChange={(e) => setNewName(e.target.value)}
          style={{ flex: '0 0 200px', background: 'var(--tm-input-bg, rgba(0,0,0,0.3))', border: '1px solid rgba(132,157,188,0.2)', borderRadius: '6px', padding: '7px 10px', color: '#fff', fontSize: '12px' }}
        />
        <input
          type="password"
          placeholder="API key"
          value={newKey}
          onChange={(e) => setNewKey(e.target.value)}
          style={{ flex: 1, background: 'var(--tm-input-bg, rgba(0,0,0,0.3))', border: '1px solid rgba(132,157,188,0.2)', borderRadius: '6px', padding: '7px 10px', color: '#fff', fontSize: '12px' }}
        />
        <button
          onClick={handleCreate}
          disabled={creating || !newName.trim() || !newKey.trim()}
          style={{ padding: '7px 16px', background: 'var(--tm-accent)', border: 'none', borderRadius: '6px', color: '#fff', fontSize: '12px', fontWeight: 600, cursor: creating ? 'not-allowed' : 'pointer', opacity: creating ? 0.6 : 1 }}
        >
          {creating ? 'Adding…' : 'Add key'}
        </button>
      </div>
      {createErr && <p style={{ margin: '6px 0 0', fontSize: '12px', color: '#e05252' }}>{createErr}</p>}
    </div>
  );
}
