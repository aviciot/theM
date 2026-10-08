'use client';
import { useEffect, useState, useCallback } from 'react';
import { themApi, type GatewayClient, type GatewayProfile, type GatewayProfileStep, type GatewayPolicy, type GatewayPolicyInput, type GatewayRequest } from '@/lib/api';
import type { MiddlewareDef } from '@/lib/apiTypes';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';

type Tab = 'clients' | 'profiles' | 'policy' | 'requests';

const ACCENT = '#7c3aed';
const ACCENT_BG = 'rgba(124,58,237,0.08)';

// ── Shared ────────────────────────────────────────────────────────────────────

function StatusBadge({ status }: { status: string }) {
  const color =
    status === 'ok'          ? '#22c55e'
    : status === 'blocked'   ? '#ef4444'
    : status === 'rate_limited' ? '#f59e0b'
    : '#94a3b8';
  return (
    <span style={{
      display: 'inline-block', padding: '1px 8px', borderRadius: 999,
      background: color + '22', color, fontSize: 12, fontWeight: 600,
    }}>{status}</span>
  );
}

// ── Clients tab ───────────────────────────────────────────────────────────────

function ClientsTab() {
  const [clients, setClients]   = useState<GatewayClient[]>([]);
  const [profiles, setProfiles] = useState<GatewayProfile[]>([]);
  const [loading, setLoading]   = useState(true);
  const [newLabel, setNewLabel] = useState('');
  const [creating, setCreating] = useState(false);
  const [newToken, setNewToken] = useState<string | null>(null);
  const [assigning, setAssigning] = useState<string | null>(null); // client id being patched
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [c, p] = await Promise.all([
        themApi.listGatewayClients(),
        themApi.listGatewayProfiles(),
      ]);
      setClients(c);
      setProfiles(p);
    } catch { setError('Failed to load clients'); }
    finally { setLoading(false); }
  }, []);

  useEffect(() => { load(); }, [load]);

  async function handleCreate() {
    if (!newLabel.trim()) return;
    setCreating(true);
    setError('');
    try {
      const result = await themApi.createGatewayClient({ label: newLabel.trim() });
      setNewToken(result.token ?? null);
      setNewLabel('');
      await load();
    } catch { setError('Failed to create client'); }
    finally { setCreating(false); }
  }

  async function handleProfileChange(clientId: string, profileId: string) {
    setAssigning(clientId);
    try {
      await themApi.patchGatewayClient(clientId, { profile_id: profileId || null });
      setClients(prev => prev.map(c =>
        c.id === clientId ? { ...c, profile_id: profileId || null } : c
      ));
    } catch { setError('Failed to update profile'); }
    finally { setAssigning(null); }
  }

  async function handleDelete(id: string) {
    if (!confirm('Delete this client? Its bearer token will stop working immediately.')) return;
    try {
      await themApi.deleteGatewayClient(id);
      setClients(prev => prev.filter(c => c.id !== id));
    } catch { setError('Failed to delete client'); }
  }

  return (
    <div>
      {newToken && (
        <div style={{ background: '#d1fae5', border: '1px solid #34d399', borderRadius: 8, padding: 16, marginBottom: 20 }}>
          <div style={{ fontWeight: 600, marginBottom: 8, color: '#065f46' }}>
            Bearer token — copy now, it will not be shown again.
          </div>
          <code style={{ wordBreak: 'break-all', fontSize: 13, color: '#064e3b' }}>{newToken}</code>
          <button onClick={() => setNewToken(null)} style={{ marginLeft: 16, color: '#065f46', cursor: 'pointer', background: 'none', border: 'none', fontSize: 13 }}>✕ dismiss</button>
        </div>
      )}

      <div style={{ display: 'flex', gap: 8, marginBottom: 20 }}>
        <input
          placeholder="Client label e.g. reporting-cron"
          value={newLabel}
          onChange={e => setNewLabel(e.target.value)}
          onKeyDown={e => e.key === 'Enter' && handleCreate()}
          style={{ flex: 1, padding: '8px 12px', borderRadius: 6, border: '1px solid #334155', background: '#1e293b', color: '#f1f5f9', fontSize: 14 }}
        />
        <button
          onClick={handleCreate}
          disabled={creating || !newLabel.trim()}
          style={{ padding: '8px 16px', borderRadius: 6, background: ACCENT, color: '#fff', border: 'none', cursor: 'pointer', fontWeight: 600, opacity: creating ? 0.6 : 1 }}
        >
          {creating ? '…' : 'Create client'}
        </button>
      </div>

      {error && <div style={{ color: '#ef4444', marginBottom: 12 }}>{error}</div>}

      {loading ? <div style={{ color: '#94a3b8' }}>Loading…</div> : (
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 14 }}>
          <thead>
            <tr style={{ borderBottom: '1px solid #334155', color: '#94a3b8' }}>
              <th style={{ textAlign: 'left', padding: '8px 12px' }}>Label</th>
              <th style={{ textAlign: 'left', padding: '8px 12px' }}>Profile</th>
              <th style={{ textAlign: 'left', padding: '8px 12px' }}>Last seen</th>
              <th style={{ textAlign: 'left', padding: '8px 12px' }}>Created</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {clients.length === 0 && (
              <tr><td colSpan={5} style={{ padding: 20, color: '#64748b', textAlign: 'center' }}>No clients yet</td></tr>
            )}
            {clients.map(c => (
              <tr key={c.id} style={{ borderBottom: '1px solid #1e293b' }}>
                <td style={{ padding: '10px 12px' }}>
                  <div style={{ fontWeight: 600 }}>{c.label}</div>
                  <div style={{ color: '#64748b', fontFamily: 'monospace', fontSize: 11 }}>{c.id.slice(0, 8)}…</div>
                </td>
                <td style={{ padding: '10px 12px' }}>
                  <select
                    value={c.profile_id ?? ''}
                    disabled={assigning === c.id}
                    onChange={e => handleProfileChange(c.id, e.target.value)}
                    style={{
                      padding: '5px 8px', borderRadius: 6, border: '1px solid #334155',
                      background: '#0f172a', color: '#f1f5f9', fontSize: 13,
                      cursor: 'pointer', minWidth: 140,
                      opacity: assigning === c.id ? 0.5 : 1,
                    }}
                  >
                    <option value="">— none —</option>
                    {profiles.map(p => (
                      <option key={p.id} value={p.id}>{p.name}</option>
                    ))}
                  </select>
                </td>
                <td style={{ padding: '10px 12px', color: '#94a3b8' }}>{c.last_seen ? new Date(c.last_seen).toLocaleString() : '—'}</td>
                <td style={{ padding: '10px 12px', color: '#94a3b8' }}>{new Date(c.created_at).toLocaleDateString()}</td>
                <td style={{ padding: '10px 12px', textAlign: 'right' }}>
                  <button onClick={() => handleDelete(c.id)} style={{ color: '#ef4444', background: 'none', border: 'none', cursor: 'pointer', fontSize: 13 }}>Delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

// ── Profile step editor (expand-in-place) ─────────────────────────────────────

const SLUG_LABELS: Record<string, string> = {
  pii_redact:     'PII Redact',
  prompt_inject:  'Prompt Injection Guard',
  guard_default:  'PII + Injection (combined)',
};

function ProfileStepEditor({ profile, defs }: { profile: GatewayProfile; defs: MiddlewareDef[] }) {
  const [steps, setSteps]       = useState<GatewayProfileStep[]>([]);
  const [loading, setLoading]   = useState(true);
  const [selectedDef, setSelectedDef] = useState('');
  const [position, setPosition] = useState(1);
  const [adding, setAdding]     = useState(false);
  const [error, setError]       = useState('');

  const loadSteps = useCallback(async () => {
    setLoading(true);
    try { setSteps(await themApi.listGatewayProfileSteps(profile.id)); }
    catch { setError('Failed to load steps'); }
    finally { setLoading(false); }
  }, [profile.id]);

  useEffect(() => { loadSteps(); }, [loadSteps]);

  // Next auto-position: max existing position + 1, minimum 1.
  useEffect(() => {
    if (steps.length > 0) {
      setPosition(Math.max(...steps.map(s => s.position)) + 1);
    }
  }, [steps]);

  async function handleAdd() {
    if (!selectedDef) return;
    setAdding(true);
    setError('');
    try {
      const step = await themApi.addGatewayProfileStep(profile.id, {
        def_id: selectedDef,
        position,
        config: {},
      });
      setSteps(prev => [...prev, step].sort((a, b) => a.position - b.position));
      setSelectedDef('');
    } catch { setError('Failed to add step'); }
    finally { setAdding(false); }
  }

  async function handleRemove(stepId: string) {
    try {
      await themApi.deleteGatewayProfileStep(stepId);
      setSteps(prev => prev.filter(s => s.id !== stepId));
    } catch { setError('Failed to remove step'); }
  }

  // Guard defs only (skip cache/file-guard — async, not hot-path usable).
  const availableDefs = defs.filter(d =>
    d.kind === 'guard' && d.slug !== 'file-guard' && d.slug !== 'file_guard'
  );

  return (
    <div style={{ marginTop: 12, padding: '12px 16px', background: '#0f172a', borderRadius: 6, border: '1px solid #1e293b' }}>
      {error && <div style={{ color: '#ef4444', marginBottom: 8, fontSize: 13 }}>{error}</div>}

      {/* Step list */}
      {loading ? (
        <div style={{ color: '#64748b', fontSize: 13 }}>Loading steps…</div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginBottom: 12 }}>
          {steps.length === 0 && (
            <div style={{ color: '#475569', fontSize: 13, fontStyle: 'italic' }}>
              No steps — requests pass through unfiltered.
            </div>
          )}
          {steps.map((s, i) => {
            const label = s.def_name || SLUG_LABELS[s.def_slug ?? ''] || s.def_slug || s.def_id;
            const isPost = s.position >= 100;
            return (
              <div key={s.id} style={{
                display: 'flex', alignItems: 'center', gap: 10,
                padding: '8px 10px', borderRadius: 6, background: '#1e293b',
              }}>
                <span style={{
                  width: 22, height: 22, borderRadius: '50%',
                  background: ACCENT_BG, color: ACCENT, fontSize: 11, fontWeight: 700,
                  display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
                }}>{i + 1}</span>
                <div style={{ flex: 1 }}>
                  <span style={{ fontSize: 13, fontWeight: 500 }}>{label}</span>
                  <span style={{
                    marginLeft: 8, fontSize: 11, padding: '1px 6px', borderRadius: 4,
                    background: isPost ? '#0284c722' : '#7c3aed22',
                    color: isPost ? '#38bdf8' : '#a78bfa',
                  }}>{isPost ? 'post-LLM' : 'pre-LLM'}</span>
                  <span style={{ marginLeft: 6, color: '#475569', fontSize: 11 }}>pos {s.position}</span>
                </div>
                <button
                  onClick={() => handleRemove(s.id)}
                  style={{ color: '#ef4444', background: 'none', border: 'none', cursor: 'pointer', fontSize: 12, padding: '2px 6px' }}
                >Remove</button>
              </div>
            );
          })}
        </div>
      )}

      {/* Add step */}
      <div style={{ display: 'flex', gap: 6, alignItems: 'center', flexWrap: 'wrap' }}>
        <select
          value={selectedDef}
          onChange={e => setSelectedDef(e.target.value)}
          style={{
            flex: 2, minWidth: 180, padding: '6px 8px', borderRadius: 6,
            border: '1px solid #334155', background: '#1e293b', color: '#f1f5f9', fontSize: 13,
          }}
        >
          <option value="">— pick a guard —</option>
          {availableDefs.map(d => (
            <option key={d.id} value={d.id}>{d.display_name || SLUG_LABELS[d.slug] || d.slug}</option>
          ))}
        </select>
        <input
          type="number"
          value={position}
          onChange={e => setPosition(parseInt(e.target.value) || 1)}
          min={1}
          title="Position (< 100 = pre-LLM, ≥ 100 = post-LLM)"
          style={{
            width: 70, padding: '6px 8px', borderRadius: 6,
            border: '1px solid #334155', background: '#1e293b', color: '#f1f5f9', fontSize: 13,
          }}
        />
        <span style={{ color: '#475569', fontSize: 11, whiteSpace: 'nowrap' }}>&lt;100 pre · ≥100 post</span>
        <button
          onClick={handleAdd}
          disabled={adding || !selectedDef}
          style={{
            padding: '6px 14px', borderRadius: 6, background: ACCENT, color: '#fff',
            border: 'none', cursor: 'pointer', fontWeight: 600, fontSize: 13,
            opacity: adding || !selectedDef ? 0.5 : 1,
          }}
        >{adding ? '…' : '+ Add step'}</button>
      </div>
    </div>
  );
}

// ── Profiles tab ──────────────────────────────────────────────────────────────

function ProfilesTab() {
  const [profiles, setProfiles]   = useState<GatewayProfile[]>([]);
  const [defs, setDefs]           = useState<MiddlewareDef[]>([]);
  const [loading, setLoading]     = useState(true);
  const [newName, setNewName]     = useState('');
  const [creating, setCreating]   = useState(false);
  const [expanded, setExpanded]   = useState<string | null>(null);
  const [error, setError]         = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [p, d] = await Promise.all([
        themApi.listGatewayProfiles(),
        themApi.listMiddlewareDefs(),
      ]);
      setProfiles(p);
      setDefs(d);
    } catch { setError('Failed to load profiles'); }
    finally { setLoading(false); }
  }, []);

  useEffect(() => { load(); }, [load]);

  async function handleCreate() {
    if (!newName.trim()) return;
    setCreating(true);
    try {
      const p = await themApi.createGatewayProfile({ name: newName.trim() });
      setNewName('');
      setProfiles(prev => [...prev, p]);
      setExpanded(p.id); // auto-expand so the user can add steps immediately
    } catch { setError('Failed to create profile'); }
    finally { setCreating(false); }
  }

  async function handleDelete(id: string) {
    if (!confirm('Delete this profile? Clients assigned to it will lose their profile binding.')) return;
    try {
      await themApi.deleteGatewayProfile(id);
      setProfiles(prev => prev.filter(p => p.id !== id));
      if (expanded === id) setExpanded(null);
    } catch { setError('Failed to delete profile'); }
  }

  return (
    <div>
      <div style={{ marginBottom: 12, color: '#94a3b8', fontSize: 14 }}>
        Profiles are named policy pipelines. Each step runs in order before (pre-LLM) or after (post-LLM) the upstream call.
        Assign a profile to a client in the Clients tab.
      </div>

      <div style={{ display: 'flex', gap: 8, marginBottom: 20 }}>
        <input
          placeholder="Profile name e.g. P2-Guarded"
          value={newName}
          onChange={e => setNewName(e.target.value)}
          onKeyDown={e => e.key === 'Enter' && handleCreate()}
          style={{ flex: 1, padding: '8px 12px', borderRadius: 6, border: '1px solid #334155', background: '#1e293b', color: '#f1f5f9', fontSize: 14 }}
        />
        <button
          onClick={handleCreate}
          disabled={creating || !newName.trim()}
          style={{ padding: '8px 16px', borderRadius: 6, background: ACCENT, color: '#fff', border: 'none', cursor: 'pointer', fontWeight: 600, opacity: creating ? 0.6 : 1 }}
        >
          {creating ? '…' : 'Create profile'}
        </button>
      </div>

      {error && <div style={{ color: '#ef4444', marginBottom: 12 }}>{error}</div>}

      {loading ? <div style={{ color: '#94a3b8' }}>Loading…</div> : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {profiles.length === 0 && <div style={{ color: '#64748b', padding: 20, textAlign: 'center' }}>No profiles yet</div>}
          {profiles.map(p => {
            const isOpen = expanded === p.id;
            return (
              <div key={p.id} style={{ background: '#1e293b', borderRadius: 8, overflow: 'hidden', border: isOpen ? `1px solid ${ACCENT}44` : '1px solid transparent' }}>
                {/* Profile header row */}
                <div
                  style={{ padding: '12px 16px', display: 'flex', alignItems: 'center', gap: 12, cursor: 'pointer' }}
                  onClick={() => setExpanded(isOpen ? null : p.id)}
                >
                  <span style={{ color: isOpen ? ACCENT : '#94a3b8', fontSize: 14, width: 16, textAlign: 'center', flexShrink: 0 }}>
                    {isOpen ? '▾' : '▸'}
                  </span>
                  <div style={{ flex: 1 }}>
                    <div style={{ fontWeight: 600 }}>{p.name}</div>
                    <div style={{ color: '#64748b', fontSize: 12, fontFamily: 'monospace' }}>{p.id}</div>
                  </div>
                  <span style={{
                    padding: '2px 8px', borderRadius: 999, fontSize: 12,
                    background: p.enabled ? '#22c55e22' : '#94a3b822',
                    color: p.enabled ? '#22c55e' : '#94a3b8',
                  }}>{p.enabled ? 'enabled' : 'disabled'}</span>
                  <button
                    onClick={e => { e.stopPropagation(); handleDelete(p.id); }}
                    style={{ color: '#ef4444', background: 'none', border: 'none', cursor: 'pointer', fontSize: 13 }}
                  >Delete</button>
                </div>
                {/* Expandable step editor */}
                {isOpen && <ProfileStepEditor profile={p} defs={defs} />}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ── Policy tab ────────────────────────────────────────────────────────────────

function PolicyTab() {
  const [policy, setPolicy] = useState<GatewayPolicy | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);

  // Form state
  const [modelsText, setModelsText] = useState('');
  const [aliasesText, setAliasesText] = useState('{}');
  const [maxTokens, setMaxTokens] = useState('');
  const [monthlyBudget, setMonthlyBudget] = useState('');

  useEffect(() => {
    themApi.getGatewayPolicy().then(p => {
      setPolicy(p);
      setModelsText((p.allowed_models ?? []).join('\n'));
      setAliasesText(JSON.stringify(p.model_aliases ?? {}, null, 2));
      setMaxTokens(p.max_tokens_per_request != null ? String(p.max_tokens_per_request) : '');
      setMonthlyBudget(p.monthly_budget_usd != null ? String(p.monthly_budget_usd) : '');
    }).catch(() => setError('Failed to load policy')).finally(() => setLoading(false));
  }, []);

  async function handleSave() {
    setSaving(true);
    setError('');
    setSaved(false);
    let aliases: Record<string, string> = {};
    try { aliases = JSON.parse(aliasesText || '{}'); } catch { setError('model_aliases must be valid JSON'); setSaving(false); return; }
    const models = modelsText.split('\n').map(s => s.trim()).filter(Boolean);
    const body: GatewayPolicyInput = {
      allowed_models: models,
      model_aliases: aliases,
      max_tokens_per_request: maxTokens ? parseInt(maxTokens) : null,
      monthly_budget_usd: monthlyBudget ? parseFloat(monthlyBudget) : null,
    };
    try {
      const updated = await themApi.putGatewayPolicy(body);
      setPolicy(updated);
      setSaved(true);
    } catch { setError('Failed to save policy'); }
    finally { setSaving(false); }
  }

  if (loading) return <div style={{ color: '#94a3b8' }}>Loading…</div>;

  return (
    <div style={{ maxWidth: 600 }}>
      <div style={{ marginBottom: 12, color: '#94a3b8', fontSize: 14 }}>
        Leave <strong>Allowed models</strong> empty to permit all models (fail-open / allow-all).
        Aliases resolve before the allowlist check.
      </div>

      {error && <div style={{ color: '#ef4444', marginBottom: 12 }}>{error}</div>}
      {saved && <div style={{ color: '#22c55e', marginBottom: 12 }}>Policy saved.</div>}

      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        <label>
          <div style={{ color: '#94a3b8', fontSize: 13, marginBottom: 4 }}>Allowed models (one per line, empty = allow all)</div>
          <textarea
            rows={5}
            value={modelsText}
            onChange={e => setModelsText(e.target.value)}
            style={{ width: '100%', padding: '8px 12px', borderRadius: 6, border: '1px solid #334155', background: '#1e293b', color: '#f1f5f9', fontSize: 13, fontFamily: 'monospace', resize: 'vertical', boxSizing: 'border-box' }}
            placeholder={'claude-sonnet-5\ngpt-4o'}
          />
        </label>

        <label>
          <div style={{ color: '#94a3b8', fontSize: 13, marginBottom: 4 }}>Model aliases (JSON object)</div>
          <textarea
            rows={5}
            value={aliasesText}
            onChange={e => setAliasesText(e.target.value)}
            style={{ width: '100%', padding: '8px 12px', borderRadius: 6, border: '1px solid #334155', background: '#1e293b', color: '#f1f5f9', fontSize: 13, fontFamily: 'monospace', resize: 'vertical', boxSizing: 'border-box' }}
            placeholder={'{\n  "fast": "claude-haiku-4-5-20251001"\n}'}
          />
        </label>

        <div style={{ display: 'flex', gap: 16 }}>
          <label style={{ flex: 1 }}>
            <div style={{ color: '#94a3b8', fontSize: 13, marginBottom: 4 }}>Max tokens per request (empty = no cap)</div>
            <input
              type="number"
              value={maxTokens}
              onChange={e => setMaxTokens(e.target.value)}
              style={{ width: '100%', padding: '8px 12px', borderRadius: 6, border: '1px solid #334155', background: '#1e293b', color: '#f1f5f9', fontSize: 14, boxSizing: 'border-box' }}
              placeholder="e.g. 4096"
            />
          </label>
          <label style={{ flex: 1 }}>
            <div style={{ color: '#94a3b8', fontSize: 13, marginBottom: 4 }}>Monthly budget USD (empty = no cap)</div>
            <input
              type="number"
              step="0.01"
              value={monthlyBudget}
              onChange={e => setMonthlyBudget(e.target.value)}
              style={{ width: '100%', padding: '8px 12px', borderRadius: 6, border: '1px solid #334155', background: '#1e293b', color: '#f1f5f9', fontSize: 14, boxSizing: 'border-box' }}
              placeholder="e.g. 50.00"
            />
          </label>
        </div>

        <button
          onClick={handleSave}
          disabled={saving}
          style={{ alignSelf: 'flex-start', padding: '9px 20px', borderRadius: 6, background: ACCENT, color: '#fff', border: 'none', cursor: 'pointer', fontWeight: 600, opacity: saving ? 0.6 : 1 }}
        >
          {saving ? 'Saving…' : 'Save policy'}
        </button>
      </div>
    </div>
  );
}

// ── Requests tab ──────────────────────────────────────────────────────────────

function RequestsTab() {
  const [reqs, setReqs] = useState<GatewayRequest[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    themApi.listGatewayRequests(200).then(setReqs)
      .catch(() => setError('Failed to load requests'))
      .finally(() => setLoading(false));
  }, []);

  const totalCost = reqs.reduce((s, r) => s + r.cost_usd, 0);
  const totalIn   = reqs.reduce((s, r) => s + r.tokens_in, 0);
  const totalOut  = reqs.reduce((s, r) => s + r.tokens_out, 0);

  return (
    <div>
      {error && <div style={{ color: '#ef4444', marginBottom: 12 }}>{error}</div>}

      {!loading && reqs.length > 0 && (
        <div style={{ display: 'flex', gap: 16, marginBottom: 20 }}>
          {[
            { label: 'Requests', value: reqs.length.toLocaleString() },
            { label: 'Tokens in', value: totalIn.toLocaleString() },
            { label: 'Tokens out', value: totalOut.toLocaleString() },
            { label: 'Cost (USD)', value: '$' + totalCost.toFixed(4) },
          ].map(s => (
            <div key={s.label} style={{ background: '#1e293b', borderRadius: 8, padding: '12px 16px', flex: 1 }}>
              <div style={{ color: '#94a3b8', fontSize: 12 }}>{s.label}</div>
              <div style={{ fontSize: 20, fontWeight: 700, marginTop: 4 }}>{s.value}</div>
            </div>
          ))}
        </div>
      )}

      {loading ? <div style={{ color: '#94a3b8' }}>Loading…</div> : (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr style={{ borderBottom: '1px solid #334155', color: '#94a3b8' }}>
                {['Time', 'Client', 'Model served', 'Status', 'Tokens in', 'Tokens out', 'Cost'].map(h => (
                  <th key={h} style={{ textAlign: 'left', padding: '8px 12px', whiteSpace: 'nowrap' }}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {reqs.length === 0 && (
                <tr><td colSpan={7} style={{ padding: 20, color: '#64748b', textAlign: 'center' }}>No requests yet</td></tr>
              )}
              {reqs.map(r => (
                <tr key={r.id} style={{ borderBottom: '1px solid #1e293b' }}>
                  <td style={{ padding: '8px 12px', color: '#94a3b8', whiteSpace: 'nowrap' }}>{new Date(r.created_at).toLocaleString()}</td>
                  <td style={{ padding: '8px 12px' }}>{r.client_label ?? '—'}</td>
                  <td style={{ padding: '8px 12px', fontFamily: 'monospace' }}>{r.model_served ?? r.model_requested ?? '—'}</td>
                  <td style={{ padding: '8px 12px' }}><StatusBadge status={r.status} /></td>
                  <td style={{ padding: '8px 12px', textAlign: 'right' }}>{r.tokens_in.toLocaleString()}</td>
                  <td style={{ padding: '8px 12px', textAlign: 'right' }}>{r.tokens_out.toLocaleString()}</td>
                  <td style={{ padding: '8px 12px', textAlign: 'right' }}>${r.cost_usd.toFixed(6)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function GatewayPage() {
  const [tab, setTab] = useState<Tab>('clients');

  const tabs: { key: Tab; label: string }[] = [
    { key: 'clients', label: 'Clients' },
    { key: 'profiles', label: 'Profiles' },
    { key: 'policy', label: 'Policy' },
    { key: 'requests', label: 'Requests' },
  ];

  return (
    <AuthGuard>
      <div style={{ minHeight: '100vh', background: '#0f172a', color: '#f1f5f9' }}>
        <Sidebar />
        <main style={{ marginLeft: '260px', padding: 32, overflowY: 'auto' }}>
          <div style={{ marginBottom: 24 }}>
            <h1 style={{ fontSize: 22, fontWeight: 700, margin: 0 }}>LLM Gateway</h1>
            <p style={{ color: '#94a3b8', marginTop: 6, fontSize: 14 }}>
              Manage closed-agent clients, governance policy, and inspect request history.
            </p>
          </div>

          {/* Tabs */}
          <div style={{ display: 'flex', gap: 0, marginBottom: 24, borderBottom: '1px solid #1e293b' }}>
            {tabs.map(t => (
              <button
                key={t.key}
                onClick={() => setTab(t.key)}
                style={{
                  padding: '10px 20px', background: 'none', border: 'none', cursor: 'pointer',
                  color: tab === t.key ? ACCENT : '#94a3b8',
                  fontWeight: tab === t.key ? 700 : 400,
                  borderBottom: tab === t.key ? `2px solid ${ACCENT}` : '2px solid transparent',
                  fontSize: 14, transition: 'color 0.15s',
                }}
              >{t.label}</button>
            ))}
          </div>

          {tab === 'clients'  && <ClientsTab />}
          {tab === 'profiles' && <ProfilesTab />}
          {tab === 'policy'   && <PolicyTab />}
          {tab === 'requests' && <RequestsTab />}
        </main>
      </div>
    </AuthGuard>
  );
}
