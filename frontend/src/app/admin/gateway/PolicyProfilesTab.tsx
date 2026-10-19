'use client';
import { useEffect, useState } from 'react';
import { themApi, type GatewayPolicyProfile, type GatewayPolicyProfileInput, type LLMProviderOut } from '@/lib/api';

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
                      {p.allowed_models?.length ? `${p.allowed_models.length} model${p.allowed_models.length !== 1 ? 's' : ''} allowed` : 'all models allowed'}
                      {Object.keys(p.model_aliases).length > 0 && ` • ${Object.keys(p.model_aliases).length} alias${Object.keys(p.model_aliases).length !== 1 ? 'es' : ''}`}
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
  const [loggingLevel, setLoggingLevel] = useState(profile?.logging_level || 'minimal');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  // Provider models for the allowed-models picker
  const [providers, setProviders] = useState<LLMProviderOut[]>([]);
  const [selectedModels, setSelectedModels] = useState<Set<string>>(
    new Set(profile?.allowed_models ?? [])
  );

  useEffect(() => {
    themApi.listMyLLMProviders().then(setProviders).catch(() => {});
  }, []);

  // All models from enabled providers, grouped by provider
  const providerGroups = providers
    .filter(p => p.enabled && p.allowed_models?.length)
    .map(p => ({ name: p.display_name || p.name, models: p.allowed_models }));
  const allModels = providerGroups.flatMap(g => g.models);

  function toggleModel(model: string) {
    setSelectedModels(prev => {
      const next = new Set(prev);
      if (next.has(model)) next.delete(model); else next.add(model);
      return next;
    });
  }

  function toggleAll() {
    if (selectedModels.size === allModels.length) {
      setSelectedModels(new Set());
    } else {
      setSelectedModels(new Set(allModels));
    }
  }

  async function handleSave() {
    if (!name.trim()) { setError('Name is required'); return; }
    setSaving(true);
    setError('');
    const aliasObj: Record<string, string> = {};
    aliases.forEach(a => { if (a.key.trim() && a.value.trim()) aliasObj[a.key.trim()] = a.value.trim(); });
    const body: GatewayPolicyProfileInput = {
      name: name.trim(),
      allowed_models: selectedModels.size > 0 ? Array.from(selectedModels) : [],
      model_aliases: aliasObj,
      max_tokens_per_request: maxTokens ? parseInt(maxTokens) : null,
      monthly_budget_usd: monthlyBudget ? parseFloat(monthlyBudget) : null,
      logging_level: loggingLevel,
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
      <div style={{ background: '#1e293b', borderRadius: 10, padding: 24, maxWidth: 620, width: '90%', maxHeight: '90vh', overflowY: 'auto' }} onClick={e => e.stopPropagation()}>
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

          {/* Allowed models — from tenant LLM providers */}
          <div>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 6 }}>
              <div style={{ fontSize: 13, color: '#94a3b8' }}>
                Allowed Models
                <span style={{ fontSize: 11, color: '#64748b', marginLeft: 6 }}>
                  {selectedModels.size === 0 ? '(empty = all models allowed)' : `${selectedModels.size} selected`}
                </span>
              </div>
              {allModels.length > 0 && (
                <button onClick={toggleAll} style={{ fontSize: 11, color: ACCENT, background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}>
                  {selectedModels.size === allModels.length ? 'Deselect all' : 'Select all'}
                </button>
              )}
            </div>
            {providerGroups.length === 0 ? (
              <div style={{ padding: '10px 12px', background: '#0f172a', borderRadius: 6, fontSize: 12, color: '#64748b', border: '1px solid #1e293b' }}>
                No enabled LLM providers with models configured.{' '}
                <a href="/admin/settings?tab=llm_providers" style={{ color: ACCENT }}>Configure in Settings →</a>
              </div>
            ) : (
              <div style={{ background: '#0f172a', border: '1px solid #1e293b', borderRadius: 6, padding: '8px 12px', display: 'flex', flexDirection: 'column', gap: 10 }}>
                {providerGroups.map(g => (
                  <div key={g.name}>
                    <div style={{ fontSize: 11, fontWeight: 700, color: '#64748b', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 6 }}>{g.name}</div>
                    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                      {g.models.map(m => {
                        const checked = selectedModels.has(m);
                        return (
                          <label key={m} style={{ display: 'flex', alignItems: 'center', gap: 5, cursor: 'pointer', padding: '4px 8px', borderRadius: 5, border: `1px solid ${checked ? ACCENT : '#334155'}`, background: checked ? 'rgba(124,58,237,0.12)' : 'transparent' }}>
                            <input type="checkbox" checked={checked} onChange={() => toggleModel(m)} style={{ accentColor: ACCENT, cursor: 'pointer' }} />
                            <span style={{ fontSize: 12, fontFamily: 'monospace', color: checked ? '#c4b5fd' : '#94a3b8' }}>{m}</span>
                          </label>
                        );
                      })}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>

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
                {selectedModels.size > 0 ? (
                  <select
                    value={a.value}
                    onChange={e => setAliases(prev => prev.map((x, j) => j === i ? { ...x, value: e.target.value } : x))}
                    style={{ flex: 1, padding: '6px 10px', borderRadius: 6, border: '1px solid #334155', background: '#0f172a', color: a.value ? '#f1f5f9' : '#64748b', fontSize: 12, fontFamily: 'monospace' }}
                  >
                    <option value="">— pick a model —</option>
                    {Array.from(selectedModels).sort().map(m => (
                      <option key={m} value={m}>{m}</option>
                    ))}
                  </select>
                ) : (
                  <input
                    type="text"
                    placeholder="Target model (e.g. claude-haiku-4-5)"
                    value={a.value}
                    onChange={e => setAliases(prev => prev.map((x, j) => j === i ? { ...x, value: e.target.value } : x))}
                    style={{ flex: 1, padding: '6px 10px', borderRadius: 6, border: '1px solid #334155', background: '#0f172a', color: '#f1f5f9', fontSize: 12, fontFamily: 'monospace' }}
                  />
                )}
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
              <div style={{ fontSize: 13, color: '#94a3b8', marginBottom: 4 }}>Max tokens per request</div>
              <input
                type="number"
                value={maxTokens}
                onChange={e => setMaxTokens(e.target.value)}
                placeholder="e.g. 4096 (empty = no cap)"
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
                placeholder="e.g. 50.00 (empty = no cap)"
                style={{ width: '100%', padding: '8px 12px', borderRadius: 6, border: '1px solid #334155', background: '#0f172a', color: '#f1f5f9', fontSize: 14, boxSizing: 'border-box' }}
              />
            </label>
          </div>

          <div>
            <div style={{ fontSize: 13, color: '#94a3b8', marginBottom: 4 }}>
              Logging Level
              <span style={{ fontSize: 11, color: '#64748b', marginLeft: 6 }}>
                (controls what data is captured)
              </span>
            </div>
            <select
              value={loggingLevel}
              onChange={e => setLoggingLevel(e.target.value)}
              style={{ width: '100%', padding: '8px 12px', borderRadius: 6, border: '1px solid #334155', background: '#0f172a', color: '#f1f5f9', fontSize: 14, boxSizing: 'border-box', cursor: 'pointer' }}
            >
              <option value="minimal">Minimal — Basic metrics only (tokens, cost, latency)</option>
              <option value="metrics">Metrics — Adds per-node timing and status</option>
              <option value="debug">Debug — Adds full request/response I/O</option>
              <option value="audit">Audit — Adds bodies + PII/security decisions</option>
            </select>
            <div style={{ fontSize: 11, color: '#64748b', marginTop: 6, lineHeight: 1.4 }}>
              <strong>minimal</strong> writes gateway_requests only. <strong>audit</strong> captures full request/response bodies.
            </div>
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
