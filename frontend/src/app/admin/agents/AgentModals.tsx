'use client';
import type { Agent, AgentSkill, DiscoverResult, OrchestratorFull, ScanResult } from '@/lib/api';
import type { FormState } from './agentTypes';
import type { CardDiff } from './agentTypes';
import { nestedSurface, inputStyle } from './AgentCard';
import { cardVersion, cardProvider, cardDocUrl, cardAuth, riskColors, statusIcon, scoreRingColor, timeAgo } from './agentUtils';

function Modal({ title, onClose, wide, children }: { title: string; onClose: () => void; wide?: boolean; children: React.ReactNode }) {
  return (
    <div style={{ position: 'fixed', inset: 0, zIndex: 100, background: 'rgba(0,0,0,0.65)', backdropFilter: 'blur(4px)', display: 'flex', alignItems: 'center', justifyContent: 'center' }} onClick={onClose}>
      <div style={{ position: 'relative', background: 'linear-gradient(145deg, rgba(255,255,255,.028) 0%, rgba(255,255,255,.008) 36%, rgba(0,0,0,.045) 100%), var(--tm-card-chrome)', border: '1px solid var(--tm-modal-border)', borderRadius: '18px', padding: '32px', width: wide ? '760px' : '580px', maxHeight: '90vh', overflowY: 'auto', boxShadow: '0 24px 64px rgba(0,0,0,.55), 0 6px 18px var(--tm-inset-deep), inset 0 1px 0 rgba(255,255,255,.06), inset 0 -1px 0 var(--tm-inset-deep)' }} onClick={(e) => e.stopPropagation()}>
        <div style={{ position: 'absolute', inset: '1px', borderRadius: '17px', pointerEvents: 'none', boxShadow: 'inset 0 0 0 1px rgba(255,255,255,.018)' }} />
        <div style={{ position: 'absolute', top: 0, left: '24px', right: '24px', height: '1px', pointerEvents: 'none', background: 'linear-gradient(90deg, transparent, rgba(40,215,238,.38), transparent)', borderRadius: '1px' }} />
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
          <h3 style={{ fontSize: '17px', fontWeight: 700, color: 'var(--tm-text)', letterSpacing: '-0.01em' }}>{title}</h3>
          <button onClick={onClose} style={{ background: 'linear-gradient(145deg, rgba(255,255,255,.018), rgba(0,0,0,.05)), var(--tm-inset)', border: '1px solid var(--tm-input-border)', boxShadow: 'inset 0 1px 0 rgba(255,255,255,.025), inset 0 -1px 0 rgba(0,0,0,.2)', borderRadius: '8px', cursor: 'pointer', color: 'var(--tm-text-muted)', fontSize: '16px', width: '32px', height: '32px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>✕</button>
        </div>
        {children}
      </div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: '16px' }}>
      <label style={{ display: 'block', fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', marginBottom: '6px', textTransform: 'uppercase', letterSpacing: '0.06em' }}>{label}</label>
      {children}
    </div>
  );
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ fontSize: '10px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.07em', marginBottom: '8px' }}>
      {children}
    </div>
  );
}

function ChangedBadge({ old: oldVal, next: newVal }: { old: string; next: string }) {
  return (
    <span style={{ fontSize: '11px' }}>
      <span style={{ color: 'var(--tm-card-text-muted)', textDecoration: 'line-through', marginRight: '6px' }}>{oldVal || '—'}</span>
      <span style={{ color: '#4edea3', fontWeight: 600 }}>{newVal || '—'}</span>
    </span>
  );
}

function DiffRow({ label, changed, oldVal, newVal }: { label: string; changed: boolean; oldVal: string; newVal: string }) {
  if (!changed && !newVal) return null;
  return (
    <div style={{ display: 'flex', gap: '8px', alignItems: 'flex-start', padding: '6px 0', borderBottom: '1px solid var(--tm-border)' }}>
      <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)', minWidth: '110px', flexShrink: 0 }}>{label}</span>
      {changed
        ? <ChangedBadge old={oldVal} next={newVal} />
        : <span style={{ fontSize: '12px', color: 'var(--tm-text)' }}>{newVal}</span>
      }
    </div>
  );
}

export interface AgentModalsProps {
  // Create/Edit modal
  showModal: boolean;
  editing: Agent | null;
  form: FormState;
  saving: boolean;
  error: string;
  discovering: boolean;
  discoverError: string;
  onCloseModal: () => void;
  onFieldChange: (k: keyof FormState, v: unknown) => void;
  onDiscover: () => void;
  onSave: () => void;
  // Discover popup
  discoverPopup: { agent: Agent; result: DiscoverResult; diff: CardDiff } | null;
  orchestrators: OrchestratorFull[];
  applyingDiscover: boolean;
  onCloseDiscoverPopup: () => void;
  onApplyDiscover: () => void;
  // Scan modal
  scanModal: { agent: Agent; result: ScanResult } | null;
  scanResults: Record<string, ScanResult | 'scanning'>;
  onCloseScanModal: () => void;
  onRescan: (agent: Agent) => void;
  // Delete modal
  deleteTarget: Agent | null;
  onCloseDelete: () => void;
  onConfirmDelete: () => void;
  // Folder name modal
  pendingFolder: { agentA: Agent; agentB: Agent } | null;
  folderNameInput: string;
  onFolderNameChange: (v: string) => void;
  onCloseFolderPrompt: () => void;
  onConfirmCreateFolder: () => void;
}

export function AgentModals({
  showModal, editing, form, saving, error, discovering, discoverError,
  onCloseModal, onFieldChange, onDiscover, onSave,
  discoverPopup, orchestrators, applyingDiscover, onCloseDiscoverPopup, onApplyDiscover,
  scanModal, scanResults, onCloseScanModal, onRescan,
  deleteTarget, onCloseDelete, onConfirmDelete,
  pendingFolder, folderNameInput, onFolderNameChange, onCloseFolderPrompt, onConfirmCreateFolder,
}: AgentModalsProps) {
  const set = (k: keyof FormState, v: unknown) => onFieldChange(k, v);

  return (
    <>
      {/* ── Create / Edit Modal ── */}
      {showModal && (
        <Modal title={editing ? `Edit — ${editing.display_name}` : 'New Agent'} onClose={onCloseModal}>
          <Field label="Endpoint URL">
            <div style={{ display: 'flex', gap: '8px' }}>
              <input style={{ ...inputStyle, flex: 1 }} value={form.endpoint_url} onChange={(e) => set('endpoint_url', e.target.value)} placeholder="http://host:port" />
              <button onClick={onDiscover} disabled={discovering} className="discover-btn" style={{ whiteSpace: 'nowrap', flexShrink: 0, padding: '8px 14px', fontSize: '13px' }}>
                {discovering ? 'Discovering…' : 'Discover'}
              </button>
            </div>
            {discoverError && <div style={{ fontSize: '11px', color: '#f87171', marginTop: '4px' }}>{discoverError}</div>}
            {form.agent_card_url && !discoverError && (
              <div style={{ fontSize: '11px', color: '#4edea3', marginTop: '4px' }}>
                Card fetched — {form.skills.length} skill{form.skills.length !== 1 ? 's' : ''} discovered
                {form.supports_streaming && ' · streaming'}{form.supports_push && ' · push'}
              </div>
            )}
          </Field>
          <Field label="Display Name">
            <input style={inputStyle} value={form.display_name} onChange={(e) => set('display_name', e.target.value)} placeholder="Echo Agent" />
          </Field>
          {!editing && (
            <Field label="Slug">
              <input style={inputStyle} value={form.slug} onChange={(e) => set('slug', e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, '_'))} placeholder="echo_agent" />
              <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', marginTop: '4px' }}>lowercase letters, numbers, underscores only</div>
            </Field>
          )}
          <Field label="Description (shown to LLM)">
            <textarea style={{ ...inputStyle, minHeight: '72px', resize: 'vertical', fontFamily: 'inherit' }} value={form.description} onChange={(e) => set('description', e.target.value)} placeholder="Echoes back any message it receives" />
          </Field>
          {form.skills.length > 0 && (
            <Field label={`Skills (${form.skills.length})`}>
              <div style={{ ...nestedSurface, borderRadius: '8px', padding: '8px 12px', maxHeight: '120px', overflowY: 'auto' }}>
                {form.skills.map((s: AgentSkill, i: number) => (
                  <div key={i} style={{ fontSize: '12px', color: 'var(--tm-text)', marginBottom: i < form.skills.length - 1 ? '6px' : 0 }}>
                    <span style={{ fontWeight: 600 }}>{s.name}</span>
                    {s.description && <span style={{ color: 'var(--tm-text-muted)' }}> — {s.description}</span>}
                  </div>
                ))}
              </div>
            </Field>
          )}
          <Field label={editing ? 'Auth Token (leave blank to keep existing)' : 'Auth Token (optional)'}>
            <input style={inputStyle} type="password" value={form.auth_token} onChange={(e) => set('auth_token', e.target.value)} placeholder="Bearer token for the agent endpoint" />
          </Field>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '12px' }}>
            <Field label="Max Concurrency">
              <input style={inputStyle} type="number" min={1} max={20} value={form.max_concurrency} onChange={(e) => set('max_concurrency', Number(e.target.value))} />
            </Field>
            <Field label="Timeout (seconds)">
              <input style={inputStyle} type="number" min={5} max={300} value={form.timeout_seconds} onChange={(e) => set('timeout_seconds', Number(e.target.value))} />
            </Field>
          </div>
          <Field label="Icon (Material Symbols name, optional)">
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
              <input style={{ ...inputStyle, flex: 1 }} value={form.icon} onChange={(e) => set('icon', e.target.value)} placeholder="e.g. hub, visibility, code — leave blank to auto-detect" />
              {form.icon && <span className="material-symbols-outlined" style={{ fontSize: '24px', color: '#00d1ff', flexShrink: 0 }}>{form.icon}</span>}
            </div>
          </Field>
          <Field label="">
            <button type="button" onClick={() => set('enabled', !form.enabled)} style={{ display: 'flex', alignItems: 'center', gap: '10px', padding: '8px 16px', borderRadius: '9px', border: 'none', cursor: 'pointer', fontSize: '14px', fontWeight: 600, background: form.enabled ? 'rgba(16,185,129,0.15)' : 'rgba(100,116,139,0.15)', color: form.enabled ? '#34d399' : 'var(--tm-card-text-muted)', transition: 'all 0.18s' }}>
              <span style={{ width: '32px', height: '18px', borderRadius: '9px', flexShrink: 0, background: form.enabled ? '#34d399' : '#475569', position: 'relative', display: 'inline-block', transition: 'background 0.18s' }}>
                <span style={{ position: 'absolute', top: '3px', left: form.enabled ? '17px' : '3px', width: '12px', height: '12px', borderRadius: '50%', background: '#fff', transition: 'left 0.18s' }} />
              </span>
              {form.enabled ? 'Enabled' : 'Disabled'}
            </button>
          </Field>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '10px' }}>
            <Field label="">
              {(() => {
                const isInternalForm = form.tags.includes('internal');
                function toggleInternal() {
                  set('tags', isInternalForm
                    ? form.tags.filter((t: string) => t !== 'internal' && t !== 'locked')
                    : [...form.tags.filter((t: string) => t !== 'internal' && t !== 'locked'), 'internal', 'locked']);
                }
                return (
                  <button type="button" onClick={toggleInternal} title="Mark as a built-in the-M system agent" style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '8px 12px', borderRadius: '9px', border: 'none', cursor: 'pointer', fontSize: '13px', fontWeight: 600, width: '100%', background: isInternalForm ? 'rgba(160,240,208,0.12)' : 'rgba(100,116,139,0.10)', color: isInternalForm ? '#a0f0d0' : 'var(--tm-card-text-muted)', outline: isInternalForm ? '1px solid rgba(160,240,208,0.3)' : '1px solid transparent', transition: 'all 0.18s' }}>
                    <span style={{ width: '28px', height: '16px', borderRadius: '8px', flexShrink: 0, background: isInternalForm ? '#a0f0d0' : '#475569', position: 'relative', display: 'inline-block', transition: 'background 0.18s' }}>
                      <span style={{ position: 'absolute', top: '2px', left: isInternalForm ? '14px' : '2px', width: '12px', height: '12px', borderRadius: '50%', background: '#fff', transition: 'left 0.18s' }} />
                    </span>
                    the-M Agent
                  </button>
                );
              })()}
            </Field>
            <Field label="">
              {(() => {
                const isInternalForm = form.tags.includes('internal');
                const isLockedForm = form.tags.includes('locked') || isInternalForm;
                function toggleLocked() {
                  if (isInternalForm) return;
                  set('tags', isLockedForm ? form.tags.filter((t: string) => t !== 'locked') : [...form.tags, 'locked']);
                }
                return (
                  <button type="button" onClick={toggleLocked} disabled={isInternalForm} title={isInternalForm ? 'the-M agents are always locked' : 'Prevent this agent from being deleted'} style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '8px 12px', borderRadius: '9px', border: 'none', cursor: isInternalForm ? 'not-allowed' : 'pointer', fontSize: '13px', fontWeight: 600, width: '100%', background: isLockedForm ? 'rgba(148,163,184,0.14)' : 'rgba(100,116,139,0.10)', color: isLockedForm ? '#94a3b8' : 'var(--tm-card-text-muted)', outline: isLockedForm && !isInternalForm ? '1px solid rgba(148,163,184,0.3)' : '1px solid transparent', opacity: isInternalForm ? 0.55 : 1, transition: 'all 0.18s' }}>
                    <span style={{ width: '28px', height: '16px', borderRadius: '8px', flexShrink: 0, background: isLockedForm ? '#94a3b8' : '#475569', position: 'relative', display: 'inline-block', transition: 'background 0.18s' }}>
                      <span style={{ position: 'absolute', top: '2px', left: isLockedForm ? '14px' : '2px', width: '12px', height: '12px', borderRadius: '50%', background: '#fff', transition: 'left 0.18s' }} />
                    </span>
                    Locked
                  </button>
                );
              })()}
            </Field>
          </div>
          {error && <div style={{ padding: '10px 12px', borderRadius: '8px', background: 'rgba(220,38,38,.08)', border: '1px solid rgba(220,38,38,.2)', color: '#f87171', fontSize: '13px', marginBottom: '16px' }}>{error}</div>}
          <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end', marginTop: '8px' }}>
            <button className="ghost-btn" onClick={onCloseModal} style={{ padding: '8px 20px', fontSize: '14px' }}>Cancel</button>
            <button onClick={onSave} disabled={saving} style={{ padding: '8px 22px', borderRadius: '9px', border: 'none', background: saving ? 'rgba(99,102,241,.5)' : 'var(--tm-accent)', color: '#fff', cursor: saving ? 'not-allowed' : 'pointer', fontSize: '14px', fontWeight: 600, opacity: saving ? 0.7 : 1 }}>
              {saving ? 'Saving…' : (editing ? 'Save Changes' : 'Create Agent')}
            </button>
          </div>
        </Modal>
      )}

      {/* ── Discover popup ── */}
      {discoverPopup && (() => {
        const { agent, result, diff } = discoverPopup;
        const newCard = (result.agent_card ?? {}) as Record<string, unknown>;
        const version = cardVersion(result.agent_card);
        const provider = cardProvider(result.agent_card);
        const docUrl = cardDocUrl(result.agent_card);
        const authSchemes = cardAuth(result.agent_card);
        const affectedOrchestrators = orchestrators.filter(o => o.allowed_agent_ids.includes(agent.id));

        return (
          <Modal wide title={`Agent Card — ${result.display_name || agent.display_name}`} onClose={onCloseDiscoverPopup}>
            <div style={{ padding: '10px 14px', borderRadius: '9px', marginBottom: '20px', background: diff.hasChanges ? 'linear-gradient(145deg, rgba(230,184,92,.10), rgba(180,131,9,.04))' : 'linear-gradient(145deg, rgba(66,217,139,.10), rgba(42,181,109,.04))', border: `1px solid ${diff.hasChanges ? 'rgba(230,184,92,.28)' : 'rgba(66,217,139,.28)'}`, boxShadow: 'inset 0 1px 0 rgba(255,255,255,.025)', display: 'flex', alignItems: 'center', gap: '8px' }}>
              <span style={{ fontSize: '16px' }}>{diff.hasChanges ? '⚠️' : '✓'}</span>
              <span style={{ fontSize: '13px', fontWeight: 600, color: diff.hasChanges ? '#e6b85c' : '#4edea3' }}>
                {diff.hasChanges ? 'Changes detected — review and save to update this agent' : 'Up to date — no changes since last sync'}
              </span>
            </div>

            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '24px' }}>
              <div>
                <SectionLabel>Agent Info</SectionLabel>
                <div style={{ ...nestedSurface, borderRadius: '8px', overflow: 'hidden', marginBottom: '16px' }}>
                  <DiffRow label="Name" changed={diff.displayName.changed} oldVal={diff.displayName.old} newVal={diff.displayName.new} />
                  {version && <DiffRow label="Version" changed={diff.version.changed} oldVal={diff.version.old} newVal={version} />}
                  {provider && <DiffRow label="Provider" changed={diff.provider.changed} oldVal={diff.provider.old} newVal={provider} />}
                  {docUrl && (
                    <div style={{ display: 'flex', gap: '8px', alignItems: 'center', padding: '6px 0 6px 0', borderBottom: '1px solid var(--tm-border)' }}>
                      <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)', minWidth: '110px' }}>Docs</span>
                      <a href={docUrl} target="_blank" rel="noreferrer" style={{ fontSize: '12px', color: '#60a5fa' }}>{docUrl}</a>
                    </div>
                  )}
                  <DiffRow label="Streaming" changed={diff.streaming.changed} oldVal={diff.streaming.old ? 'yes' : 'no'} newVal={diff.streaming.new ? 'yes' : 'no'} />
                  <DiffRow label="Push" changed={diff.push.changed} oldVal={diff.push.old ? 'yes' : 'no'} newVal={diff.push.new ? 'yes' : 'no'} />
                  {result.category && <DiffRow label="Category" changed={result.category !== (agent.category ?? '')} oldVal={agent.category || '—'} newVal={result.category} />}
                  {result.icon && <DiffRow label="Icon" changed={result.icon !== (agent.icon ?? '')} oldVal={agent.icon || '—'} newVal={result.icon} />}
                  {authSchemes.length > 0 && (
                    <div style={{ display: 'flex', gap: '8px', alignItems: 'center', padding: '6px 0' }}>
                      <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)', minWidth: '110px' }}>Auth</span>
                      <span style={{ fontSize: '12px', color: 'var(--tm-text)' }}>{authSchemes.join(', ')}</span>
                    </div>
                  )}
                </div>

                <SectionLabel>Card URL</SectionLabel>
                <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', fontFamily: 'monospace', wordBreak: 'break-all', marginBottom: '16px' }}>
                  {result.agent_card_url}
                </div>

                {affectedOrchestrators.length > 0 && (
                  <>
                    <SectionLabel>Used by orchestrators</SectionLabel>
                    <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                      {affectedOrchestrators.map(o => (
                        <div key={o.id} style={{ fontSize: '12px', padding: '4px 8px', borderRadius: '6px', background: 'rgba(230,184,92,.08)', color: '#e6b85c', border: '1px solid rgba(230,184,92,.2)' }}>
                          {o.display_name || o.name}
                        </div>
                      ))}
                    </div>
                  </>
                )}
              </div>

              <div>
                <SectionLabel>Description</SectionLabel>
                <div style={{ ...nestedSurface, padding: '10px 12px', borderRadius: '8px', marginBottom: '16px', border: `1px solid ${diff.description.changed ? 'rgba(66,217,139,.28)' : 'rgba(132,157,188,.12)'}` }}>
                  {diff.description.changed && (
                    <div style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', textDecoration: 'line-through', marginBottom: '6px', whiteSpace: 'pre-wrap' }}>{diff.description.old || '—'}</div>
                  )}
                  <div style={{ fontSize: '12px', color: diff.description.changed ? '#4edea3' : 'var(--tm-card-text)', whiteSpace: 'pre-wrap', lineHeight: 1.5 }}>
                    {result.description || '—'}
                  </div>
                </div>

                <SectionLabel>Skills ({result.skills.length}){diff.skills.changed && <span style={{ color: '#e6b85c', marginLeft: '6px', textTransform: 'none', fontSize: '10px' }}>changed</span>}</SectionLabel>
                <div style={{ display: 'flex', flexDirection: 'column', gap: '6px', maxHeight: '280px', overflowY: 'auto' }}>
                  {result.skills.length === 0 && <div style={{ fontSize: '12px', color: 'var(--tm-text-muted)' }}>No skills declared</div>}
                  {result.skills.map((s, i) => {
                    const skillCard = ((newCard.skills ?? []) as Record<string, unknown>[])[i] ?? {};
                    const inputModes = Array.isArray(skillCard.inputModes) ? (skillCard.inputModes as string[]) : [];
                    const outputModes = Array.isArray(skillCard.outputModes) ? (skillCard.outputModes as string[]) : [];
                    return (
                      <div key={i} style={{ ...nestedSurface, padding: '8px 10px', borderRadius: '8px' }}>
                        <div style={{ fontSize: '12px', fontWeight: 600, color: 'var(--tm-text)', marginBottom: '2px' }}>{s.name}</div>
                        {s.description && <div style={{ fontSize: '11px', color: 'var(--tm-card-text-hint)', lineHeight: 1.4, marginBottom: '4px' }}>{s.description}</div>}
                        <div style={{ display: 'flex', gap: '4px', flexWrap: 'wrap' }}>
                          {(s.tags ?? []).map((t, ti) => (
                            <span key={ti} style={{ fontSize: '10px', padding: '1px 5px', borderRadius: '3px', background: 'rgba(167,139,250,.12)', color: '#a78bfa', border: '1px solid rgba(167,139,250,.18)' }}>{t}</span>
                          ))}
                          {inputModes.map((m, mi) => (
                            <span key={`in-${mi}`} style={{ fontSize: '10px', padding: '1px 5px', borderRadius: '3px', background: 'rgba(96,165,250,.10)', color: '#60a5fa', border: '1px solid rgba(96,165,250,.18)' }}>in:{m}</span>
                          ))}
                          {outputModes.map((m, mi) => (
                            <span key={`out-${mi}`} style={{ fontSize: '10px', padding: '1px 5px', borderRadius: '3px', background: 'rgba(78,222,163,.10)', color: '#4edea3', border: '1px solid rgba(78,222,163,.18)' }}>out:{m}</span>
                          ))}
                        </div>
                      </div>
                    );
                  })}
                </div>
              </div>
            </div>

            <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end', marginTop: '24px', paddingTop: '16px', borderTop: '1px solid rgba(132,158,190,.12)' }}>
              <button className="ghost-btn" onClick={onCloseDiscoverPopup} style={{ padding: '8px 20px', fontSize: '14px' }}>Close</button>
              {diff.hasChanges && (
                <button onClick={onApplyDiscover} disabled={applyingDiscover} className="save-pulse" style={{ padding: '8px 24px', borderRadius: '9px', border: '1px solid rgba(167,139,250,.48)', background: 'linear-gradient(145deg, rgba(167,139,250,.18), rgba(124,58,237,.10)), var(--tm-inset)', boxShadow: '0 6px 18px rgba(124,58,237,.14), inset 0 1px 0 rgba(255,255,255,.08)', color: '#c4b5fd', cursor: applyingDiscover ? 'not-allowed' : 'pointer', fontSize: '14px', fontWeight: 700, opacity: applyingDiscover ? 0.7 : 1 }}>
                  {applyingDiscover ? 'Saving…' : 'Save Changes'}
                </button>
              )}
            </div>
          </Modal>
        );
      })()}

      {/* ── Security scan detail modal ── */}
      {scanModal && (() => {
        const { agent, result } = scanModal;
        const rc = riskColors(result.risk);
        const ringColor = scoreRingColor(result.score);
        return (
          <Modal wide title="" onClose={onCloseScanModal}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '24px', marginTop: '-8px' }}>
              <div style={{ width: '40px', height: '40px', borderRadius: '11px', flexShrink: 0, background: 'radial-gradient(circle at 30% 20%, rgba(40,215,238,.20), transparent 60%), linear-gradient(145deg, rgba(27,47,68,.96), rgba(8,20,35,.96))', border: '1px solid rgba(40,215,238,.35)', boxShadow: '0 5px 14px var(--tm-inset-deep), 0 0 12px rgba(40,215,238,.10), inset 0 1px 0 var(--tm-card-border)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: '20px' }}>🛡</div>
              <div>
                <div style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.07em', marginBottom: '2px' }}>Security Report</div>
                <div style={{ fontSize: '17px', fontWeight: 700, color: 'var(--tm-text)', letterSpacing: '-0.01em' }}>{agent.display_name}</div>
              </div>
            </div>

            <div style={{ display: 'flex', alignItems: 'center', gap: '16px', marginBottom: '24px', padding: '16px 18px', borderRadius: '12px', background: 'linear-gradient(145deg, rgba(255,255,255,.020) 0%, rgba(255,255,255,.006) 36%, rgba(0,0,0,.04) 100%), rgba(8,18,32,.80)', border: `1px solid ${rc.border}`, boxShadow: `0 6px 18px rgba(0,0,0,.22), 0 0 24px ${rc.glow}, inset 0 1px 0 rgba(255,255,255,.04)` }}>
              <div className="score-ring" style={{ background: `radial-gradient(circle at 40% 30%, ${ringColor}22, transparent 60%), linear-gradient(145deg, rgba(20,38,60,.96), rgba(8,18,32,.96))`, border: `2px solid ${ringColor}55` }}>
                <span style={{ fontSize: '24px', fontWeight: 800, color: ringColor, lineHeight: 1 }}>{result.score}</span>
                <span style={{ fontSize: '9px', fontWeight: 700, color: `${ringColor}99`, textTransform: 'uppercase', letterSpacing: '0.04em' }}>/100</span>
              </div>
              <div style={{ flex: 1 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '8px' }}>
                  <span style={{ fontSize: '10px', fontWeight: 700, padding: '3px 10px', borderRadius: '5px', letterSpacing: '0.06em', textTransform: 'uppercase', background: rc.bg, border: `1px solid ${rc.border}`, boxShadow: 'inset 0 1px 0 rgba(255,255,255,.04)', color: rc.color }}>
                    {result.risk} risk
                  </span>
                </div>
                <p style={{ fontSize: '14px', color: 'var(--tm-card-text)', lineHeight: 1.55, fontWeight: 500, margin: 0 }}>{result.summary}</p>
              </div>
            </div>

            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '24px' }}>
              <div>
                <SectionLabel>Findings</SectionLabel>
                <div style={{ display: 'flex', flexDirection: 'column', gap: '0' }}>
                  {result.findings.map((f, i) => {
                    const si = statusIcon(f.status);
                    const frc = riskColors(f.risk);
                    return (
                      <div key={i} className="finding-card" style={{ ...nestedSurface, borderRadius: '10px', padding: '11px 13px', marginBottom: '7px' }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '4px' }}>
                          <span style={{ width: '22px', height: '22px', borderRadius: '6px', flexShrink: 0, background: `${si.color}18`, border: `1px solid ${si.color}30`, display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: '12px', color: si.color, fontWeight: 700 }}>{si.icon}</span>
                          <span style={{ fontSize: '13px', fontWeight: 700, color: 'var(--tm-text)', flex: 1 }}>{f.label}</span>
                          <span style={{ fontSize: '9px', fontWeight: 700, padding: '2px 7px', borderRadius: '4px', background: frc.bg, border: `1px solid ${frc.border}`, color: frc.color, textTransform: 'uppercase', letterSpacing: '0.05em' }}>{f.risk}</span>
                        </div>
                        <p style={{ fontSize: '12px', color: 'var(--tm-text-muted)', margin: '0 0 4px 30px', lineHeight: 1.5 }}>{f.detail}</p>
                        {f.recommendation !== 'No action needed.' && (
                          <p style={{ fontSize: '11px', color: '#60a5fa', margin: '0 0 0 30px', lineHeight: 1.4 }}>
                            <span style={{ opacity: 0.6 }}>→</span> {f.recommendation}
                          </p>
                        )}
                      </div>
                    );
                  })}
                </div>
              </div>

              <div>
                <SectionLabel>HTTP Probes</SectionLabel>
                <div style={{ marginBottom: '20px' }}>
                  {[
                    { label: 'TLS', value: result.http_probes.tls },
                    { label: 'Auth Required', value: result.http_probes.auth_required },
                    { label: 'Reachable', value: result.http_probes.reachable ? 'pass' : 'fail' },
                  ].map(({ label, value }) => {
                    const pass = value === 'pass';
                    return (
                      <div key={label} className="probe-row" style={{ ...nestedSurface, borderRadius: '8px', marginBottom: '6px' }}>
                        <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)', fontWeight: 500 }}>{label}</span>
                        <span style={{ fontSize: '10px', fontWeight: 700, padding: '2px 9px', borderRadius: '4px', letterSpacing: '0.04em', background: pass ? 'linear-gradient(145deg, rgba(66,217,139,.14), rgba(42,181,109,.06))' : 'linear-gradient(145deg, rgba(220,38,38,.14), rgba(185,28,28,.06))', border: `1px solid ${pass ? 'rgba(66,217,139,.24)' : 'rgba(220,38,38,.24)'}`, boxShadow: 'inset 0 1px 0 rgba(255,255,255,.03)', color: pass ? '#4edea3' : '#f87171' }}>
                          {pass ? '✓ pass' : '✗ fail'}
                        </span>
                      </div>
                    );
                  })}
                </div>

                <SectionLabel>Scanned</SectionLabel>
                <p style={{ fontSize: '12px', color: 'var(--tm-text-muted)', marginBottom: '20px', lineHeight: 1.5 }}>
                  {timeAgo(result.scanned_at)}<br />
                  <span style={{ fontSize: '11px', opacity: 0.7 }}>{new Date(result.scanned_at).toLocaleString()}</span>
                </p>

                <SectionLabel>Agent</SectionLabel>
                <div style={{ ...nestedSurface, borderRadius: '8px', padding: '8px 12px' }}>
                  <div style={{ fontSize: '12px', color: 'var(--tm-text)', fontWeight: 600, marginBottom: '3px' }}>
                    <code style={{ background: 'rgba(40,215,238,.08)', border: '1px solid rgba(40,215,238,.15)', padding: '1px 6px', borderRadius: '4px', color: '#28d7ee', fontSize: '11px' }}>{agent.slug}</code>
                  </div>
                  <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', wordBreak: 'break-all', lineHeight: 1.4 }}>{agent.endpoint_url}</div>
                </div>
              </div>
            </div>

            <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end', marginTop: '24px', paddingTop: '16px', borderTop: '1px solid rgba(132,158,190,.10)' }}>
              <button className="ghost-btn" onClick={onCloseScanModal} style={{ padding: '8px 20px', fontSize: '14px' }}>Close</button>
              <button className="btn-primary-scan" onClick={() => onRescan(agent)} disabled={scanResults[agent.id] === 'scanning'}>
                <span style={{ fontSize: '15px' }}>🛡</span>
                {scanResults[agent.id] === 'scanning' ? 'Scanning…' : 'Re-scan'}
              </button>
            </div>
          </Modal>
        );
      })()}

      {/* ── Delete confirm ── */}
      {deleteTarget && (
        <Modal title="Delete Agent" onClose={onCloseDelete}>
          <p style={{ color: 'var(--tm-text)', marginBottom: '24px', lineHeight: 1.6 }}>
            Delete <strong style={{ color: '#f87171' }}>{deleteTarget.display_name}</strong>? This cannot be undone and will remove it from any orchestrators that use it.
          </p>
          <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end' }}>
            <button className="ghost-btn" onClick={onCloseDelete} style={{ padding: '8px 20px', fontSize: '14px' }}>Cancel</button>
            <button className="delete-btn" onClick={onConfirmDelete} style={{ padding: '8px 20px', fontSize: '14px', fontWeight: 700 }}>Delete</button>
          </div>
        </Modal>
      )}

      {/* ── Name folder modal ── */}
      {pendingFolder && (
        <Modal title="Name this folder" onClose={onCloseFolderPrompt}>
          <p style={{ color: 'var(--tm-card-text-muted)', fontSize: '13px', marginBottom: '20px', lineHeight: 1.55 }}>
            Grouping <strong style={{ color: 'var(--tm-card-text)' }}>{pendingFolder.agentA.display_name}</strong> and <strong style={{ color: 'var(--tm-card-text)' }}>{pendingFolder.agentB.display_name}</strong> into a folder.
          </p>
          <Field label="Folder name">
            <input
              style={inputStyle}
              autoFocus
              value={folderNameInput}
              onChange={(e) => onFolderNameChange(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter') onConfirmCreateFolder(); if (e.key === 'Escape') onCloseFolderPrompt(); }}
              placeholder="e.g. Research Agents"
            />
          </Field>
          <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end', marginTop: '8px' }}>
            <button className="ghost-btn" onClick={onCloseFolderPrompt} style={{ padding: '8px 20px', fontSize: '14px' }}>Cancel</button>
            <button onClick={onConfirmCreateFolder} style={{ padding: '8px 22px', borderRadius: '9px', border: 'none', background: 'var(--tm-accent)', color: '#fff', cursor: 'pointer', fontSize: '14px', fontWeight: 600 }}>Create Folder</button>
          </div>
        </Modal>
      )}
    </>
  );
}
