'use client';
import { useEffect, useState } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type Agent, type AgentSkill, type DiscoverResult } from '@/lib/api';

const EMPTY_FORM = {
  slug: '',
  display_name: '',
  description: '',
  transport: 'a2a_async',
  endpoint_url: '',
  auth_token: '',
  max_concurrency: 3,
  timeout_seconds: 60,
  enabled: true,
  skills: [] as AgentSkill[],
  supports_streaming: false,
  supports_push: false,
  agent_card: null as Record<string, unknown> | null,
  agent_card_url: '',
};

type FormState = typeof EMPTY_FORM;

function Modal({
  title, onClose, children,
}: { title: string; onClose: () => void; children: React.ReactNode }) {
  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 100,
      background: 'rgba(0,0,0,0.5)', display: 'flex', alignItems: 'center', justifyContent: 'center',
    }} onClick={onClose}>
      <div style={{
        background: 'var(--tm-surface)', border: '1px solid var(--tm-border)',
        borderRadius: '16px', padding: '32px', width: '560px', maxHeight: '90vh',
        overflowY: 'auto',
      }} onClick={(e) => e.stopPropagation()}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
          <h3 style={{ fontSize: '18px', fontWeight: 700, color: 'var(--tm-text)' }}>{title}</h3>
          <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--tm-text-muted)', fontSize: '20px' }}>✕</button>
        </div>
        {children}
      </div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: '16px' }}>
      <label style={{ display: 'block', fontSize: '12px', fontWeight: 600, color: 'var(--tm-text-muted)', marginBottom: '6px', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
        {label}
      </label>
      {children}
    </div>
  );
}

const inputStyle: React.CSSProperties = {
  width: '100%', padding: '8px 12px', borderRadius: '8px',
  border: '1px solid var(--tm-border)', background: 'var(--tm-surface-2)',
  fontSize: '14px', color: 'var(--tm-text)', boxSizing: 'border-box',
};

export default function AdminAgentsPage() {
  const [agents, setAgents] = useState<Agent[]>([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);
  const [editing, setEditing] = useState<Agent | null>(null);
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [deleteTarget, setDeleteTarget] = useState<Agent | null>(null);
  const [testResults, setTestResults] = useState<Record<string, { ok: boolean; latency_ms: number; detail: string } | 'testing'>>({});
  const [rowDiscoverResults, setRowDiscoverResults] = useState<Record<string, 'discovering'>>({});
  const [discoverPopup, setDiscoverPopup] = useState<{ agent: Agent; result: DiscoverResult } | null>(null);
  const [applyingDiscover, setApplyingDiscover] = useState(false);
  const [discovering, setDiscovering] = useState(false);
  const [discoverError, setDiscoverError] = useState('');

  const reload = () => themApi.agents().then(setAgents).finally(() => setLoading(false));

  useEffect(() => { reload(); }, []);

  function openCreate() {
    setEditing(null);
    setForm(EMPTY_FORM);
    setError('');
    setDiscoverError('');
    setShowModal(true);
  }

  function openEdit(agent: Agent) {
    setEditing(agent);
    setForm({
      slug: agent.slug,
      display_name: agent.display_name,
      description: agent.description || '',
      transport: agent.transport,
      endpoint_url: agent.endpoint_url,
      auth_token: '',
      max_concurrency: agent.max_concurrency,
      timeout_seconds: agent.timeout_seconds,
      enabled: agent.enabled,
      skills: agent.skills || [],
      supports_streaming: agent.supports_streaming || false,
      supports_push: agent.supports_push || false,
      agent_card: agent.agent_card || null,
      agent_card_url: agent.agent_card_url || '',
    });
    setError('');
    setDiscoverError('');
    setShowModal(true);
  }

  async function handleDiscover() {
    if (!form.endpoint_url.trim()) {
      setDiscoverError('Enter an endpoint URL first');
      return;
    }
    setDiscovering(true);
    setDiscoverError('');
    try {
      const result = await themApi.discoverAgent({
        endpoint_url: form.endpoint_url.trim(),
        auth_token: form.auth_token || undefined,
      });
      if (!result.ok) {
        setDiscoverError(result.detail || 'Discovery failed');
        return;
      }
      setForm((f) => ({
        ...f,
        display_name: result.display_name || f.display_name,
        slug: editing ? f.slug : (result.suggested_slug || f.slug),
        description: result.description || f.description,
        skills: result.skills,
        supports_streaming: result.supports_streaming,
        supports_push: result.supports_push,
        agent_card: result.agent_card,
        agent_card_url: result.agent_card_url,
      }));
    } catch (e: unknown) {
      setDiscoverError(e instanceof Error ? e.message : 'Discovery failed');
    } finally {
      setDiscovering(false);
    }
  }

  async function handleSave() {
    setSaving(true);
    setError('');
    try {
      const body: Record<string, unknown> = { ...form };
      if (!body.auth_token) delete body.auth_token;
      if (editing) {
        delete body.slug;
        await themApi.updateAgent(editing.id, body);
      } else {
        await themApi.createAgent(body);
      }
      setShowModal(false);
      reload();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  }

  async function handleTest(agent: Agent) {
    setTestResults((r) => ({ ...r, [agent.id]: 'testing' }));
    try {
      const result = await themApi.testAgent(agent.id);
      setTestResults((r) => ({ ...r, [agent.id]: result }));
    } catch (e: unknown) {
      setTestResults((r) => ({ ...r, [agent.id]: { ok: false, latency_ms: 0, detail: e instanceof Error ? e.message : 'Test failed' } }));
    }
  }

  async function handleRowDiscover(agent: Agent) {
    setRowDiscoverResults((r) => ({ ...r, [agent.id]: 'discovering' }));
    try {
      const result = await themApi.discoverAgent({ endpoint_url: agent.endpoint_url });
      if (!result.ok) {
        alert(`Discovery failed: ${result.detail}`);
        return;
      }
      setDiscoverPopup({ agent, result });
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : 'Discovery failed');
    } finally {
      setRowDiscoverResults((r) => { const n = { ...r }; delete n[agent.id]; return n; });
    }
  }

  async function handleApplyDiscover() {
    if (!discoverPopup) return;
    setApplyingDiscover(true);
    try {
      await themApi.updateAgent(discoverPopup.agent.id, {
        display_name: discoverPopup.result.display_name || discoverPopup.agent.display_name,
        description: discoverPopup.result.description || discoverPopup.agent.description,
        skills: discoverPopup.result.skills,
        supports_streaming: discoverPopup.result.supports_streaming,
        supports_push: discoverPopup.result.supports_push,
        agent_card: discoverPopup.result.agent_card,
        agent_card_url: discoverPopup.result.agent_card_url,
      });
      setDiscoverPopup(null);
      reload();
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : 'Apply failed');
    } finally {
      setApplyingDiscover(false);
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    try {
      await themApi.deleteAgent(deleteTarget.id);
      setDeleteTarget(null);
      reload();
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : 'Delete failed');
    }
  }

  const set = (k: keyof FormState, v: unknown) => setForm((f) => ({ ...f, [k]: v }));

  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <main style={{ marginLeft: '260px', flex: 1 }}>
          {/* Header */}
          <header style={{
            position: 'sticky', top: 0, zIndex: 30, height: '56px',
            background: 'var(--tm-topbar)', borderBottom: '1px solid var(--tm-topbar-border)',
            display: 'flex', alignItems: 'center', justifyContent: 'space-between',
            padding: '0 32px',
          }}>
            <div>
              <h2 style={{ fontSize: '18px', fontWeight: 700, color: 'var(--tm-accent)' }}>Agents</h2>
              <p style={{ fontSize: '11px', color: 'var(--tm-text-muted)' }}>Manage A2A agent connectors</p>
            </div>
            <button onClick={openCreate} style={{
              display: 'flex', alignItems: 'center', gap: '6px',
              padding: '8px 16px', borderRadius: '8px', border: 'none', cursor: 'pointer',
              background: 'var(--tm-accent)', color: '#fff', fontSize: '13px', fontWeight: 600,
            }}>
              <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>add</span>
              New Agent
            </button>
          </header>

          <div style={{ padding: '32px' }}>
            <div style={{ background: 'var(--tm-surface)', border: '1px solid var(--tm-border)', borderRadius: '12px', overflow: 'hidden' }}>
              <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                <thead>
                  <tr style={{ borderBottom: '1px solid var(--tm-border)' }}>
                    {['Name', 'Slug', 'Skills', 'Endpoint', 'Status', ''].map((h) => (
                      <th key={h} style={{
                        padding: '10px 16px', textAlign: 'left',
                        fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)',
                        textTransform: 'uppercase', letterSpacing: '0.06em',
                        background: 'var(--tm-surface-2)',
                      }}>{h}</th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {loading && (
                    <tr><td colSpan={6} style={{ padding: '32px', textAlign: 'center', color: 'var(--tm-text-subtle)' }}>Loading…</td></tr>
                  )}
                  {!loading && agents.length === 0 && (
                    <tr><td colSpan={6} style={{ padding: '32px', textAlign: 'center', color: 'var(--tm-text-subtle)' }}>No agents yet — click New Agent to add one</td></tr>
                  )}
                  {agents.map((agent, i) => (
                    <tr key={agent.id}
                      style={{ borderBottom: i < agents.length - 1 ? '1px solid var(--tm-border-subtle)' : 'none' }}
                      onMouseEnter={(e) => (e.currentTarget.style.background = 'var(--tm-surface-2)')}
                      onMouseLeave={(e) => (e.currentTarget.style.background = 'transparent')}
                    >
                      <td style={{ padding: '12px 16px' }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
                          <div style={{
                            width: '32px', height: '32px', borderRadius: '8px', flexShrink: 0,
                            background: 'var(--tm-accent-bg)', color: 'var(--tm-accent)',
                            display: 'flex', alignItems: 'center', justifyContent: 'center',
                          }}>
                            <span className="material-symbols-outlined" style={{ fontSize: '16px' }}>smart_toy</span>
                          </div>
                          <div>
                            <div style={{ fontSize: '14px', fontWeight: 600, color: 'var(--tm-text)' }}>{agent.display_name}</div>
                            {agent.description && <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', marginTop: '1px' }}>{agent.description.split('\n')[0].slice(0, 60)}{agent.description.split('\n')[0].length > 60 ? '…' : ''}</div>}
                          </div>
                        </div>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        <code style={{ fontSize: '12px', color: 'var(--tm-text-muted)', background: 'var(--tm-surface-2)', padding: '2px 6px', borderRadius: '4px' }}>
                          {agent.slug}
                        </code>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        {agent.skills && agent.skills.length > 0 ? (
                          <span style={{ fontSize: '11px', color: 'var(--tm-text-muted)' }} title={agent.skills.map((s) => s.name).join(', ')}>
                            {agent.skills.length} skill{agent.skills.length !== 1 ? 's' : ''}
                          </span>
                        ) : (
                          <span style={{ fontSize: '11px', color: 'var(--tm-text-subtle)' }}>—</span>
                        )}
                      </td>
                      <td style={{ padding: '12px 16px', maxWidth: '200px' }}>
                        <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', display: 'block' }}>
                          {agent.endpoint_url}
                        </span>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        <span style={{
                          fontSize: '10px', fontWeight: 700, padding: '3px 8px', borderRadius: '4px',
                          background: agent.enabled ? 'rgba(0,118,80,.12)' : 'rgba(107,114,128,.1)',
                          color: agent.enabled ? '#005b3d' : '#6b7280',
                        }}>
                          {agent.enabled ? 'Enabled' : 'Disabled'}
                        </span>
                      </td>
                      <td style={{ padding: '12px 16px' }}>
                        <div style={{ display: 'flex', flexDirection: 'column', gap: '6px', alignItems: 'flex-end' }}>
                          <div style={{ display: 'flex', gap: '8px' }}>
                            <button onClick={() => handleRowDiscover(agent)} disabled={!!rowDiscoverResults[agent.id]} style={{
                              padding: '5px 12px', borderRadius: '6px', border: '1px solid rgba(167,139,250,.4)',
                              background: 'transparent', cursor: rowDiscoverResults[agent.id] ? 'not-allowed' : 'pointer',
                              fontSize: '12px', color: '#a78bfa',
                              opacity: rowDiscoverResults[agent.id] ? 0.6 : 1,
                            }}>
                              {rowDiscoverResults[agent.id] ? 'Discovering…' : 'Discover'}
                            </button>
                            <button onClick={() => handleTest(agent)} disabled={testResults[agent.id] === 'testing'} style={{
                              padding: '5px 12px', borderRadius: '6px', border: '1px solid var(--tm-border)',
                              background: 'transparent', cursor: 'pointer', fontSize: '12px', color: 'var(--tm-accent)',
                              opacity: testResults[agent.id] === 'testing' ? 0.6 : 1,
                            }}>
                              {testResults[agent.id] === 'testing' ? 'Testing…' : 'Test'}
                            </button>
                            <button onClick={() => openEdit(agent)} style={{
                              padding: '5px 12px', borderRadius: '6px', border: '1px solid var(--tm-border)',
                              background: 'transparent', cursor: 'pointer', fontSize: '12px', color: 'var(--tm-text)',
                            }}>Edit</button>
                            <button onClick={() => setDeleteTarget(agent)} style={{
                              padding: '5px 12px', borderRadius: '6px', border: '1px solid rgba(220,38,38,.3)',
                              background: 'transparent', cursor: 'pointer', fontSize: '12px', color: '#dc2626',
                            }}>Delete</button>
                          </div>
                          {testResults[agent.id] && testResults[agent.id] !== 'testing' && (() => {
                            const r = testResults[agent.id] as { ok: boolean; latency_ms: number; detail: string };
                            return (
                              <div style={{
                                fontSize: '11px', padding: '3px 8px', borderRadius: '4px', maxWidth: '280px', textAlign: 'right',
                                background: r.ok ? 'rgba(0,118,80,.08)' : 'rgba(220,38,38,.08)',
                                color: r.ok ? '#005b3d' : '#dc2626',
                              }}>
                                {r.ok ? `✓ ${r.latency_ms}ms — ` : '✗ '}{r.detail}
                              </div>
                            );
                          })()}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </main>

        {/* Create / Edit Modal */}
        {showModal && (
          <Modal title={editing ? `Edit — ${editing.display_name}` : 'New Agent'} onClose={() => setShowModal(false)}>
            {/* Endpoint URL + Discover — always first so Discover can populate everything else */}
            <Field label="Endpoint URL">
              <div style={{ display: 'flex', gap: '8px' }}>
                <input
                  style={{ ...inputStyle, flex: 1 }}
                  value={form.endpoint_url}
                  onChange={(e) => set('endpoint_url', e.target.value)}
                  placeholder="http://host:port"
                />
                <button
                  onClick={handleDiscover}
                  disabled={discovering}
                  style={{
                    padding: '8px 14px', borderRadius: '8px', border: '1px solid var(--tm-border)',
                    background: 'var(--tm-surface-2)', cursor: discovering ? 'not-allowed' : 'pointer',
                    fontSize: '12px', fontWeight: 600, color: 'var(--tm-accent)',
                    whiteSpace: 'nowrap', opacity: discovering ? 0.6 : 1, flexShrink: 0,
                  }}
                >
                  {discovering ? 'Discovering…' : 'Discover'}
                </button>
              </div>
              {discoverError && (
                <div style={{ fontSize: '11px', color: '#dc2626', marginTop: '4px' }}>{discoverError}</div>
              )}
              {form.agent_card_url && !discoverError && (
                <div style={{ fontSize: '11px', color: '#005b3d', marginTop: '4px' }}>
                  Card fetched — {form.skills.length} skill{form.skills.length !== 1 ? 's' : ''} discovered
                  {form.supports_streaming && ' · streaming'}
                  {form.supports_push && ' · push'}
                </div>
              )}
            </Field>

            <Field label="Display Name">
              <input style={inputStyle} value={form.display_name} onChange={(e) => set('display_name', e.target.value)} placeholder="Echo Agent" />
            </Field>

            {!editing && (
              <Field label="Slug">
                <input
                  style={inputStyle}
                  value={form.slug}
                  onChange={(e) => set('slug', e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, '_'))}
                  placeholder="echo_agent"
                />
                <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', marginTop: '4px' }}>lowercase letters, numbers, underscores only</div>
              </Field>
            )}

            <Field label="Description (shown to LLM)">
              <textarea
                style={{ ...inputStyle, minHeight: '72px', resize: 'vertical', fontFamily: 'inherit' }}
                value={form.description}
                onChange={(e) => set('description', e.target.value)}
                placeholder="Echoes back any message it receives"
              />
            </Field>

            {/* Skills — read-only when discovered */}
            {form.skills.length > 0 && (
              <Field label={`Skills (${form.skills.length})`}>
                <div style={{
                  border: '1px solid var(--tm-border)', borderRadius: '8px',
                  background: 'var(--tm-surface-2)', padding: '8px 12px',
                  maxHeight: '120px', overflowY: 'auto',
                }}>
                  {form.skills.map((s, i) => (
                    <div key={i} style={{ fontSize: '12px', color: 'var(--tm-text)', marginBottom: i < form.skills.length - 1 ? '6px' : 0 }}>
                      <span style={{ fontWeight: 600 }}>{s.name}</span>
                      {s.description && <span style={{ color: 'var(--tm-text-muted)' }}> — {s.description}</span>}
                    </div>
                  ))}
                </div>
              </Field>
            )}

            <Field label={editing ? 'Auth Token (leave blank to keep existing)' : 'Auth Token (optional)'}>
              <input style={inputStyle} type="password" value={form.auth_token} onChange={(e) => set('auth_token', e.target.value)}
                placeholder="Bearer token for the agent endpoint" />
            </Field>

            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '12px' }}>
              <Field label="Max Concurrency">
                <input style={inputStyle} type="number" min={1} max={20} value={form.max_concurrency} onChange={(e) => set('max_concurrency', Number(e.target.value))} />
              </Field>
              <Field label="Timeout (seconds)">
                <input style={inputStyle} type="number" min={5} max={300} value={form.timeout_seconds} onChange={(e) => set('timeout_seconds', Number(e.target.value))} />
              </Field>
            </div>

            <Field label="Status">
              <label style={{ display: 'flex', alignItems: 'center', gap: '8px', cursor: 'pointer' }}>
                <input type="checkbox" checked={form.enabled} onChange={(e) => set('enabled', e.target.checked)} />
                <span style={{ fontSize: '14px', color: 'var(--tm-text)' }}>Enabled</span>
              </label>
            </Field>

            {error && <div style={{ padding: '10px 12px', borderRadius: '8px', background: 'rgba(220,38,38,.08)', color: '#dc2626', fontSize: '13px', marginBottom: '16px' }}>{error}</div>}

            <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end', marginTop: '8px' }}>
              <button onClick={() => setShowModal(false)} style={{
                padding: '8px 20px', borderRadius: '8px', border: '1px solid var(--tm-border)',
                background: 'transparent', cursor: 'pointer', fontSize: '14px', color: 'var(--tm-text)',
              }}>Cancel</button>
              <button onClick={handleSave} disabled={saving} style={{
                padding: '8px 20px', borderRadius: '8px', border: 'none',
                background: 'var(--tm-accent)', color: '#fff', cursor: saving ? 'not-allowed' : 'pointer',
                fontSize: '14px', fontWeight: 600, opacity: saving ? 0.7 : 1,
              }}>{saving ? 'Saving…' : (editing ? 'Save Changes' : 'Create Agent')}</button>
            </div>
          </Modal>
        )}

        {/* Discover popup */}
        {discoverPopup && (
          <Modal title={`Agent Card — ${discoverPopup.result.display_name || discoverPopup.agent.display_name}`} onClose={() => setDiscoverPopup(null)}>
            {/* Header badges */}
            <div style={{ display: 'flex', gap: '8px', flexWrap: 'wrap', marginBottom: '20px' }}>
              {discoverPopup.result.supports_streaming && (
                <span style={{ fontSize: '11px', fontWeight: 700, padding: '3px 8px', borderRadius: '4px', background: 'rgba(91,127,255,.12)', color: '#5b7fff' }}>Streaming</span>
              )}
              {discoverPopup.result.supports_push && (
                <span style={{ fontSize: '11px', fontWeight: 700, padding: '3px 8px', borderRadius: '4px', background: 'rgba(167,139,250,.12)', color: '#a78bfa' }}>Push notifications</span>
              )}
              {!discoverPopup.result.supports_streaming && !discoverPopup.result.supports_push && (
                <span style={{ fontSize: '11px', color: 'var(--tm-text-muted)' }}>No special capabilities</span>
              )}
            </div>

            {/* Description */}
            {discoverPopup.result.description && (
              <div style={{ marginBottom: '20px' }}>
                <div style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '6px' }}>Description</div>
                <p style={{ fontSize: '13px', color: 'var(--tm-text)', lineHeight: 1.6, margin: 0, whiteSpace: 'pre-wrap' }}>{discoverPopup.result.description}</p>
              </div>
            )}

            {/* Skills */}
            {discoverPopup.result.skills.length > 0 && (
              <div style={{ marginBottom: '20px' }}>
                <div style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '8px' }}>
                  Skills ({discoverPopup.result.skills.length})
                </div>
                <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
                  {discoverPopup.result.skills.map((s, i) => (
                    <div key={i} style={{ padding: '10px 12px', borderRadius: '8px', border: '1px solid var(--tm-border)', background: 'var(--tm-surface-2)' }}>
                      <div style={{ fontSize: '13px', fontWeight: 600, color: 'var(--tm-text)', marginBottom: s.description ? '4px' : 0 }}>{s.name}</div>
                      {s.description && <div style={{ fontSize: '12px', color: 'var(--tm-text-muted)', lineHeight: 1.5 }}>{s.description}</div>}
                      {s.tags && s.tags.length > 0 && (
                        <div style={{ display: 'flex', gap: '4px', flexWrap: 'wrap', marginTop: '6px' }}>
                          {s.tags.map((t: string, ti: number) => (
                            <span key={ti} style={{ fontSize: '10px', padding: '2px 6px', borderRadius: '4px', background: 'rgba(167,139,250,.1)', color: '#a78bfa' }}>{t}</span>
                          ))}
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Suggested slug */}
            <div style={{ marginBottom: '20px' }}>
              <div style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '6px' }}>Suggested Slug</div>
              <code style={{ fontSize: '13px', color: 'var(--tm-text)', background: 'var(--tm-surface-2)', padding: '4px 8px', borderRadius: '6px' }}>
                {discoverPopup.result.suggested_slug}
              </code>
            </div>

            {/* Card URL */}
            <div style={{ marginBottom: '24px' }}>
              <div style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '6px' }}>Card URL</div>
              <div style={{ fontSize: '12px', color: 'var(--tm-text-muted)', fontFamily: 'monospace', wordBreak: 'break-all' }}>{discoverPopup.result.agent_card_url}</div>
            </div>

            <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end' }}>
              <button onClick={() => setDiscoverPopup(null)} style={{
                padding: '8px 20px', borderRadius: '8px', border: '1px solid var(--tm-border)',
                background: 'transparent', cursor: 'pointer', fontSize: '14px', color: 'var(--tm-text)',
              }}>Close</button>
              <button onClick={handleApplyDiscover} disabled={applyingDiscover} style={{
                padding: '8px 20px', borderRadius: '8px', border: 'none',
                background: '#7c3aed', color: '#fff', cursor: applyingDiscover ? 'not-allowed' : 'pointer',
                fontSize: '14px', fontWeight: 600, opacity: applyingDiscover ? 0.7 : 1,
              }}>{applyingDiscover ? 'Applying…' : 'Apply to Agent'}</button>
            </div>
          </Modal>
        )}

        {/* Delete confirm */}
        {deleteTarget && (
          <Modal title="Delete Agent" onClose={() => setDeleteTarget(null)}>
            <p style={{ color: 'var(--tm-text)', marginBottom: '24px' }}>
              Delete <strong>{deleteTarget.display_name}</strong>? This cannot be undone and will remove it from any orchestrators that use it.
            </p>
            <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end' }}>
              <button onClick={() => setDeleteTarget(null)} style={{
                padding: '8px 20px', borderRadius: '8px', border: '1px solid var(--tm-border)',
                background: 'transparent', cursor: 'pointer', fontSize: '14px', color: 'var(--tm-text)',
              }}>Cancel</button>
              <button onClick={handleDelete} style={{
                padding: '8px 20px', borderRadius: '8px', border: 'none',
                background: '#dc2626', color: '#fff', cursor: 'pointer', fontSize: '14px', fontWeight: 600,
              }}>Delete</button>
            </div>
          </Modal>
        )}
      </div>
    </AuthGuard>
  );
}
