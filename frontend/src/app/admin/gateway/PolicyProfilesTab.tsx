'use client';
import { useEffect, useState } from 'react';
import { themApi, type GatewayPolicyProfile, type GatewayPolicyProfileInput } from '@/lib/api';

const ACCENT = '#7c3aed';

export function PolicyProfilesTab() {
  const [profiles, setProfiles] = useState<GatewayPolicyProfile[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [editing, setEditing] = useState<GatewayPolicyProfile | null>(null);
  const [creating, setCreating] = useState(false);

  async function load() {
    setLoading(true);
    try {
      const list = await themApi.listGatewayPolicyProfiles();
      setProfiles(list);
    } catch { setError('Failed to load profiles'); }
    finally { setLoading(false); }
  }

  useEffect(() => { load(); }, []);

  async function handleDelete(id: string) {
    if (!confirm('Delete this policy profile? Clients using it will have no policy enforcement.')) return;
    try {
      await themApi.deleteGatewayPolicyProfile(id);
      setProfiles(prev => prev.filter(p => p.id !== id));
    } catch { setError('Failed to delete profile'); }
  }

  return (
    <div style={{ maxWidth: 900 }}>
      <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <p style={{ fontSize: 13, color: '#94a3b8', margin: 0 }}>
          Create reusable policy profiles. Each client can be assigned one profile.
        </p>
        <button
          onClick={() => setCreating(true)}
          style={{ padding: '8px 16px', borderRadius: 6, background: ACCENT, color: '#fff', border: 'none', cursor: 'pointer', fontWeight: 600, fontSize: 13 }}
        >+ New Profile</button>
      </div>

      {error && <div style={{ color: '#ef4444', marginBottom: 12, padding: 10, background: 'rgba(239,68,68,0.1)', borderRadius: 6 }}>{error}</div>}

      {loading ? <div style={{ color: '#94a3b8' }}>Loading…</div> : (
        profiles.length === 0 ? (
          <div style={{ padding: 32, textAlign: 'center', color: '#64748b', background: '#1e293b', borderRadius: 8 }}>
            No policy profiles yet. Click "New Profile" to create one.
          </div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            {profiles.map(p => (
              <div key={p.id} style={{ background: '#1e293b', border: '1px solid #334155', borderRadius: 8, padding: 16 }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 8 }}>
                  <div>
                    <div style={{ fontWeight: 700, fontSize: 15 }}>{p.name}</div>
                    <div style={{ fontSize: 11, color: '#64748b', marginTop: 2 }}>
                      {Object.keys(p.model_aliases).length} alias{Object.keys(p.model_aliases).length !== 1 && 'es'}
                      {p.max_tokens_per_request && ` • ${p.max_tokens_per_request} token cap`}
                      {p.monthly_budget_usd && ` • $${p.monthly_budget_usd}/mo budget`}
                    </div>
                  </div>
                  <div style={{ display: 'flex', gap: 8 }}>
                    <button
                      onClick={() => setEditing(p)}
                      style={{ padding: '5px 12px', borderRadius: 6, border: '1px solid #334155', background: 'transparent', color: '#94a3b8', cursor: 'pointer', fontSize: 12 }}
                    >Edit</button>
                    <button
                      onClick={() => handleDelete(p.id)}
                      style={{ padding: '5px 12px', borderRadius: 6, border: '1px solid rgba(239,68,68,0.3)', background: 'transparent', color: '#ef4444', cursor: 'pointer', fontSize: 12 }}
                    >Delete</button>
                  </div>
                </div>
                {Object.keys(p.model_aliases).length > 0 && (
                  <div style={{ marginTop: 8, fontSize: 12, color: '#94a3b8' }}>
                    <strong>Aliases:</strong> {Object.entries(p.model_aliases).map(([k, v]) => `${k} → ${v}`).join(', ')}
                  </div>
                )}
              </div>
            ))}
          </div>
        )
      )}

      {(creating || editing) && <ProfileModal profile={editing} onClose={() => { setCreating(false); setEditing(null); }} onSaved={load} />}
    </div>
  );
}

function ProfileModal({ profile, onClose, onSaved }: { profile: GatewayPolicyProfile | null; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(profile?.name ?? '');
  const [aliases, setAliases] = useState<Array<{ key: string; value: string }>>(
    profile ? Object.entries(profile.model_aliases).map(([k, v]) => ({ key: k, value: v })) : []
  );
  const [maxTokens, setMaxTokens] = useState(profile?.max_tokens_per_request != null ? String(profile.max_tokens_per_request) : '');
  const [monthlyBudget, setMonthlyBudget] = useState(profile?.monthly_budget_usd != null ? String(profile.monthly_budget_usd) : '');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  async function handleSave() {
    if (!name.trim()) { setError('Name is required'); return; }
    setSaving(true);
    setError('');
    const aliasObj: Record<string, string> = {};
    aliases.forEach(a => { if (a.key.trim() && a.value.trim()) aliasObj[a.key.trim()] = a.value.trim(); });
    const body: GatewayPolicyProfileInput = {
      name: name.trim(),
      allowed_models: [], // Inherit from LLM Providers
      model_aliases: aliasObj,
      max_tokens_per_request: maxTokens ? parseInt(maxTokens) : null,
      monthly_budget_usd: monthlyBudget ? parseFloat(monthlyBudget) : null,
    };
    try {
      if (profile) {
        await themApi.updateGatewayPolicyProfile(profile.id, body);
      } else {
        await themApi.createGatewayPolicyProfile(body);
      }
      onSaved();
      onClose();
    } catch { setError('Failed to save profile'); }
    finally { setSaving(false); }
  }

  return (
    <div style={{ position: 'fixed', top: 0, left: 0, right: 0, bottom: 0, background: 'rgba(0,0,0,0.7)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 999 }} onClick={onClose}>
      <div style={{ background: '#1e293b', borderRadius: 10, padding: 24, maxWidth: 600, width: '90%', maxHeight: '90vh', overflowY: 'auto' }} onClick={e => e.stopPropagation()}>
        <h2 style={{ margin: '0 0 16px', fontSize: 18 }}>{profile ? 'Edit' : 'New'} Policy Profile</h2>

        {error && <div style={{ color: '#ef4444', marginBottom: 12, padding: 8, background: 'rgba(239,68,68,0.1)', borderRadius: 6, fontSize: 13 }}>{error}</div>}

        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <label>
            <div style={{ fontSize: 13, color: '#94a3b8', marginBottom: 4 }}>Profile Name</div>
            <input
              type="text"
              value={name}
              onChange={e => setName(e.target.value)}
              placeholder="e.g. Production, Development"
              style={{ width: '100%', padding: '8px 12px', borderRadius: 6, border: '1px solid #334155', background: '#0f172a', color: '#f1f5f9', fontSize: 14, boxSizing: 'border-box' }}
            />
          </label>

          <div>
            <div style={{ fontSize: 13, color: '#94a3b8', marginBottom: 6 }}>Model Aliases</div>
            {aliases.map((a, i) => (
              <div key={i} style={{ display: 'flex', gap: 8, marginBottom: 6 }}>
                <input
                  type="text"
                  placeholder="Alias (e.g. fast)"
                  value={a.key}
                  onChange={e => setAliases(prev => prev.map((x, j) => j === i ? { ...x, key: e.target.value } : x))}
                  style={{ flex: '0 0 120px', padding: '6px 10px', borderRadius: 6, border: '1px solid #334155', background: '#0f172a', color: '#f1f5f9', fontSize: 12 }}
                />
                <span style={{ alignSelf: 'center', color: '#64748b', fontSize: 12 }}>→</span>
                <input
                  type="text"
                  placeholder="Target model (e.g. claude-haiku-4-5)"
                  value={a.value}
                  onChange={e => setAliases(prev => prev.map((x, j) => j === i ? { ...x, value: e.target.value } : x))}
                  style={{ flex: 1, padding: '6px 10px', borderRadius: 6, border: '1px solid #334155', background: '#0f172a', color: '#f1f5f9', fontSize: 12, fontFamily: 'monospace' }}
                />
                <button
                  onClick={() => setAliases(prev => prev.filter((_, j) => j !== i))}
                  style={{ padding: '5px 8px', borderRadius: 6, border: '1px solid #334155', background: 'transparent', color: '#ef4444', cursor: 'pointer', fontSize: 11 }}
                >×</button>
              </div>
            ))}
            <button
              onClick={() => setAliases(prev => [...prev, { key: '', value: '' }])}
              style={{ padding: '5px 10px', borderRadius: 6, border: '1px solid #334155', background: 'transparent', color: '#94a3b8', cursor: 'pointer', fontSize: 12 }}
            >+ Add alias</button>
          </div>

          <div style={{ display: 'flex', gap: 12 }}>
            <label style={{ flex: 1 }}>
              <div style={{ fontSize: 13, color: '#94a3b8', marginBottom: 4 }}>Max tokens</div>
              <input
                type="number"
                value={maxTokens}
                onChange={e => setMaxTokens(e.target.value)}
                placeholder="e.g. 4096"
                style={{ width: '100%', padding: '8px 12px', borderRadius: 6, border: '1px solid #334155', background: '#0f172a', color: '#f1f5f9', fontSize: 14, boxSizing: 'border-box' }}
              />
            </label>
            <label style={{ flex: 1 }}>
              <div style={{ fontSize: 13, color: '#94a3b8', marginBottom: 4 }}>Monthly budget (USD)</div>
              <input
                type="number"
                step="0.01"
                value={monthlyBudget}
                onChange={e => setMonthlyBudget(e.target.value)}
                placeholder="e.g. 50.00"
                style={{ width: '100%', padding: '8px 12px', borderRadius: 6, border: '1px solid #334155', background: '#0f172a', color: '#f1f5f9', fontSize: 14, boxSizing: 'border-box' }}
              />
            </label>
          </div>
        </div>

        <div style={{ display: 'flex', gap: 8, marginTop: 20, justifyContent: 'flex-end' }}>
          <button
            onClick={onClose}
            style={{ padding: '8px 16px', borderRadius: 6, border: '1px solid #334155', background: 'transparent', color: '#94a3b8', cursor: 'pointer', fontSize: 13 }}
          >Cancel</button>
          <button
            onClick={handleSave}
            disabled={saving}
            style={{ padding: '8px 20px', borderRadius: 6, background: ACCENT, color: '#fff', border: 'none', cursor: 'pointer', fontWeight: 600, fontSize: 13, opacity: saving ? 0.6 : 1 }}
          >{saving ? 'Saving…' : 'Save'}</button>
        </div>
      </div>
    </div>
  );
}
