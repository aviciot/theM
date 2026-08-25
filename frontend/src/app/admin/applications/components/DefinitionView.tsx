'use client';
import { useState, useEffect } from 'react';
import { themApi, type Application, type AppDefinition, type AppDefinitionDoc, type ComponentDefinitionSummary, type ValidationReport } from '@/lib/api';
import { C, glass } from '../constants';

// ── DefinitionView ────────────────────────────────────────────────────────────
// Phase D: Application Definition editor — draft, validate, publish
// Canonical model: components[] + connections[] + entry_points[]
// Old builder (nodes/edges graph canvas) is retired.
export function DefinitionView({
  app,
  onBack,
  onAppUpdated,
}: {
  app: Application;
  onBack: () => void;
  onAppUpdated?: (updated: Application) => void;
}) {
  // ── DefinitionView state ──────────────────────────────────────────────────────
  const [defs, setDefs] = useState<AppDefinition[]>([]);
  const [activeDef, setActiveDef] = useState<AppDefinition | null>(null);
  const [draft, setDraft] = useState<AppDefinitionDoc | null>(null);
  const [isDirty, setIsDirty] = useState(false);
  const [componentDefs, setComponentDefs] = useState<ComponentDefinitionSummary[]>([]);
  const [selectedItem, setSelectedItem] = useState<{ type: 'component' | 'ep'; id: string } | null>(null);
  const [validating, setValidating] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [validationReport, setValidationReport] = useState<ValidationReport | null>(null);
  const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);
  const [showRepublishModal, setShowRepublishModal] = useState(false);

  const fieldStyle: React.CSSProperties = {
    width: '100%', padding: '10px 12px', borderRadius: 8,
    border: '1px solid rgba(255,255,255,0.12)', background: 'rgba(255,255,255,0.05)',
    color: C.text, fontSize: 14, outline: 'none', boxSizing: 'border-box',
  };

  function showToast(msg: string, ok: boolean) {
    setToast({ msg, ok });
    setTimeout(() => setToast(null), 3000);
  }

  function loadDef(def: AppDefinition) {
    setActiveDef(def);
    setDraft(JSON.parse(JSON.stringify(def.definition)));
    setIsDirty(false);
    setValidationReport(null);
    setSelectedItem(null);
  }

  async function reloadDefs(selectId?: string) {
    try {
      const list = await themApi.listDefinitions(app.id);
      setDefs(list);
      if (selectId) {
        const found = list.find(d => d.id === selectId);
        if (found) { loadDef(found); return; }
      }
      const drafts = list.filter(d => d.status === 'draft');
      if (drafts.length > 0) {
        loadDef(drafts[0]);
      } else if (list.length > 0) {
        // list is ORDER BY revision DESC so index 0 is the newest
        const latest = list[0];
        loadDef(latest);
        const seedDoc: AppDefinitionDoc = JSON.parse(JSON.stringify(latest.definition));
        const seedWithName: AppDefinitionDoc = { ...seedDoc, name: app.name };
        try {
          const res = await themApi.createDefinition(app.id, { definition: seedWithName });
          const updated = await themApi.listDefinitions(app.id);
          setDefs(updated);
          const newDef = updated.find(d => d.id === res.id);
          if (newDef) loadDef(newDef);
        } catch { /* keep showing published def if draft creation fails */ }
      } else {
        setActiveDef(null); setDraft(null);
      }
    } catch {
      showToast('Failed to load definitions', false);
    }
  }

  useEffect(() => {
    reloadDefs();
    themApi.listComponentDefinitions().then(setComponentDefs).catch(() => {});
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [app.id]);

  async function newDraft() {
    const seedDoc: AppDefinitionDoc = draft ?? {
      schema_version: 2, name: app.name,
      components: [], entry_points: [], connections: [],
    };
    const seedWithName: AppDefinitionDoc = { ...seedDoc, name: app.name };
    try {
      const res = await themApi.createDefinition(app.id, { definition: seedWithName });
      await reloadDefs(res.id);
      showToast('New draft created', true);
    } catch {
      showToast('Failed to create draft', false);
    }
  }

  async function saveDraft() {
    if (!activeDef || !draft) return;
    setSaving(true);
    try {
      await themApi.updateDefinition(app.id, activeDef.id, { definition: draft });
      setIsDirty(false);
      showToast('Saved', true);
    } catch {
      showToast('Save failed', false);
    } finally {
      setSaving(false);
    }
  }

  async function validate() {
    if (!activeDef || !draft) return;
    if (isDirty) await saveDraft();
    setValidating(true);
    try {
      const report = await themApi.validateDefinition(app.id, activeDef.id);
      setValidationReport(report);
      showToast(report.valid ? 'Valid ✓' : `${report.errors?.length ?? 0} error(s)`, report.valid);
    } catch {
      showToast('Validation failed', false);
    } finally {
      setValidating(false);
    }
  }

  async function publish() {
    if (!activeDef) return;
    setPublishing(true);
    try {
      if (isDirty) await saveDraft();
      const report = await themApi.validateDefinition(app.id, activeDef.id);
      setValidationReport(report);
      if (!report.valid) {
        showToast(`${report.errors?.length ?? 1} validation error(s)`, false);
        return;
      }
      const res = await themApi.publishDefinition(app.id, activeDef.id);
      showToast(`Published revision ${res.revision}`, true);
      await reloadDefs();
      try {
        const freshApp = await themApi.getApplication(app.id);
        onAppUpdated?.(freshApp);
      } catch { onAppUpdated?.({ ...app }); }
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'Publish failed';
      showToast(msg, false);
    } finally {
      setPublishing(false);
    }
  }

  function handlePublishClick() {
    if (app.active_revision != null) {
      setShowRepublishModal(true);
    } else {
      void publish();
    }
  }

  function addComponent(cd: ComponentDefinitionSummary) {
    if (!draft) return;
    const newComp = {
      instance_id: `${cd.kind}_${Date.now()}`,
      definition_ref: { kind: cd.kind, namespace: cd.namespace, name: cd.name, version: cd.version },
      definition_id: cd.id,
      config: {} as Record<string, unknown>,
    };
    setDraft(prev => prev ? { ...prev, components: [...prev.components, newComp] } : prev);
    setIsDirty(true);
  }

  function addEntryPoint(protocol: string) {
    if (!draft) return;
    const slug = `ep-${Date.now()}`;
    const newEP = {
      instance_id: `ep_${Date.now()}`,
      slug,
      protocol: protocol as AppDefinitionDoc['entry_points'][0]['protocol'],
      root: '',
    };
    setDraft(prev => prev ? { ...prev, entry_points: [...prev.entry_points, newEP] } : prev);
    setIsDirty(true);
  }

  function removeComponent(instanceId: string) {
    setDraft(prev => prev ? {
      ...prev,
      components: prev.components.filter(c => c.instance_id !== instanceId),
      connections: prev.connections.filter(c => c.source !== instanceId && c.target !== instanceId),
    } : prev);
    setIsDirty(true);
    if (selectedItem?.id === instanceId) setSelectedItem(null);
  }

  function removeEntryPoint(instanceId: string) {
    setDraft(prev => prev ? {
      ...prev,
      entry_points: prev.entry_points.filter(ep => ep.instance_id !== instanceId),
      connections: prev.connections.filter(c => c.source !== instanceId && c.target !== instanceId),
    } : prev);
    setIsDirty(true);
    if (selectedItem?.id === instanceId) setSelectedItem(null);
  }

  function removeConnection(idx: number) {
    setDraft(prev => prev ? {
      ...prev,
      connections: prev.connections.filter((_, i) => i !== idx),
    } : prev);
    setIsDirty(true);
  }

  function updateComponent(instanceId: string, patch: Partial<import('@/lib/api').ComponentInstance>) {
    setDraft(prev => prev ? {
      ...prev,
      components: prev.components.map(c => c.instance_id === instanceId ? { ...c, ...patch } : c),
    } : prev);
    setIsDirty(true);
  }

  function updateEntryPoint(instanceId: string, patch: Partial<import('@/lib/api').EPInstance>) {
    setDraft(prev => prev ? {
      ...prev,
      entry_points: prev.entry_points.map(ep => ep.instance_id === instanceId ? { ...ep, ...patch } : ep),
    } : prev);
    setIsDirty(true);
  }

  const selectedComp = selectedItem?.type === 'component'
    ? draft?.components.find(c => c.instance_id === selectedItem.id) ?? null
    : null;
  const selectedEP = selectedItem?.type === 'ep'
    ? draft?.entry_points.find(ep => ep.instance_id === selectedItem.id) ?? null
    : null;

  const kindColors: Record<string, string> = {
    orchestrator: C.purple, agent: C.green, middleware: C.amber, entry_point: C.cyan, tool: '#f59e0b',
  };

  const protocolOptions = ['websocket', 'sse', 'webrtc', 'a2a', 'voice'];
  const componentKinds = ['orchestrator', 'agent', 'middleware', 'entry_point', 'tool'];

  const isLive = app.active_revision != null;

  // Auto-save: trigger 3s after last change, only when a draft is loaded
  // Validation error index by instance_id
  const errorsByInstance: Record<string, string[]> = {};
  for (const err of validationReport?.errors ?? []) {
    if (err.instance_id) {
      errorsByInstance[err.instance_id] = errorsByInstance[err.instance_id] ?? [];
      errorsByInstance[err.instance_id].push(err.message);
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100vh', background: C.bg, overflow: 'hidden' }}>
      {/* Top bar */}
      <div style={{
        height: 56, flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12,
        padding: '0 20px', borderBottom: `1px solid ${C.glassBorder}`,
        background: C.surface, position: 'sticky', top: 0, zIndex: 20,
      }}>
        <button onClick={onBack} style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.textMuted, display: 'flex', alignItems: 'center', gap: 4, fontSize: 13 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 20 }}>arrow_back</span>
        </button>
        <span style={{ fontSize: 15, fontWeight: 700, color: C.text }}>{app.name}</span>
        {activeDef && (
          <span style={{
            padding: '2px 10px', borderRadius: 20, fontSize: 11, fontWeight: 700,
            background: isLive ? C.greenBg : 'rgba(208,188,255,0.12)',
            color: isLive ? C.green : C.purple,
            border: `1px solid ${isLive ? C.greenBorder : 'rgba(208,188,255,0.3)'}`,
          }}>
            {isLive ? `Rev ${app.active_revision} • live` : 'draft'}
          </span>
        )}
        <div style={{ flex: 1 }} />
        {activeDef && isDirty && (
          <button onClick={saveDraft} disabled={saving} style={{ padding: '7px 14px', borderRadius: 8, border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 700, background: 'rgba(255,255,255,0.08)', color: C.text }}>
            {saving ? 'Saving…' : 'Save'}
          </button>
        )}
        {activeDef && (
          <button
            onClick={handlePublishClick}
            disabled={publishing || saving || validating}
            style={{
              padding: '7px 16px', borderRadius: 8, cursor: publishing || saving || validating ? 'not-allowed' : 'pointer',
              fontSize: 12, fontWeight: 700,
              background: isLive ? 'rgba(245,158,11,0.15)' : C.greenBg,
              color: isLive ? '#f59e0b' : C.green,
              border: `1px solid ${isLive ? 'rgba(245,158,11,0.4)' : C.greenBorder}`,
              opacity: publishing || saving || validating ? 0.6 : 1,
            }}
          >
            {publishing ? 'Publishing…' : isLive ? 'Re-publish' : 'Publish'}
          </button>
        )}
      </div>

      {/* Validation errors banner */}
      {validationReport && !validationReport.valid && (
        <div style={{
          background: C.errorBg, borderBottom: `1px solid rgba(255,180,171,0.3)`,
          padding: '10px 20px', flexShrink: 0,
        }}>
          <div style={{ fontSize: 12, fontWeight: 700, color: C.error, marginBottom: 4 }}>Validation errors:</div>
          {(validationReport.errors ?? []).map((err, i) => (
            <div key={i} style={{ fontSize: 12, color: C.error }}>
              {err.instance_id ? `[${err.instance_id}] ` : ''}{err.message}
            </div>
          ))}
        </div>
      )}

      {/* Body — three columns */}
      <div style={{ display: 'flex', flex: 1, overflow: 'hidden' }}>

        {/* Left panel: Component Palette (260px) */}
        <div style={{ width: 260, flexShrink: 0, borderRight: `1px solid ${C.glassBorder}`, overflowY: 'auto', padding: 16, ...glass }}>
          <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, letterSpacing: '0.08em', textTransform: 'uppercase', marginBottom: 12 }}>Component Palette</div>
          {componentKinds.map(kind => {
            const items = componentDefs.filter(cd => cd.kind === kind);
            if (items.length === 0) return null;
            return (
              <div key={kind} style={{ marginBottom: 16 }}>
                <div style={{ fontSize: 10, fontWeight: 700, color: kindColors[kind] ?? C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 6 }}>{kind}</div>
                {items.map(cd => (
                  <button
                    key={cd.id}
                    onClick={() => addComponent(cd)}
                    disabled={!activeDef}
                    style={{
                      width: '100%', textAlign: 'left', padding: '8px 10px', borderRadius: 8, border: `1px solid rgba(255,255,255,0.08)`,
                      background: 'rgba(255,255,255,0.03)', color: C.text, cursor: !activeDef ? 'not-allowed' : 'pointer',
                      display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4, fontSize: 12,
                    }}
                    onMouseEnter={e => { if (activeDef) { e.currentTarget.style.background = 'rgba(255,255,255,0.07)'; e.currentTarget.style.borderColor = kindColors[kind] ?? 'rgba(255,255,255,0.2)'; } }}
                    onMouseLeave={e => { e.currentTarget.style.background = 'rgba(255,255,255,0.03)'; e.currentTarget.style.borderColor = 'rgba(255,255,255,0.08)'; }}
                  >
                    <span style={{ fontSize: 10, fontWeight: 700, padding: '1px 5px', borderRadius: 4, background: kindColors[kind] ? `${kindColors[kind]}22` : 'rgba(255,255,255,0.06)', color: kindColors[kind] ?? C.textMuted }}>
                      {cd.kind[0].toUpperCase()}
                    </span>
                    <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{cd.display_name}</span>
                    <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted, flexShrink: 0 }}>add</span>
                  </button>
                ))}
              </div>
            );
          })}
          {componentDefs.length === 0 && (
            <div style={{ fontSize: 12, color: C.textMuted, fontStyle: 'italic' }}>No components available</div>
          )}
          <div style={{ marginTop: 16, borderTop: `1px solid ${C.glassBorder}`, paddingTop: 12 }}>
            <div style={{ fontSize: 10, fontWeight: 700, color: C.cyan, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 6 }}>Entry Points</div>
            {protocolOptions.map(p => (
              <button
                key={p}
                onClick={() => addEntryPoint(p)}
                disabled={!activeDef}
                style={{
                  width: '100%', textAlign: 'left', padding: '6px 10px', borderRadius: 8, border: '1px solid rgba(0,240,255,0.15)',
                  background: 'rgba(0,240,255,0.03)', color: C.cyan, cursor: !activeDef ? 'not-allowed' : 'pointer',
                  display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4, fontSize: 12,
                }}
              >
                <span className="material-symbols-outlined" style={{ fontSize: 14 }}>add</span>
                {p}
              </button>
            ))}
          </div>
        </div>

        {/* Center panel: Definition Editor */}
        <div style={{ flex: 1, overflowY: 'auto', padding: 20, display: 'flex', flexDirection: 'column', gap: 16 }}>
          {!activeDef && (
            <div style={{ textAlign: 'center', padding: '60px 20px', color: C.textMuted }}>
              <span className="material-symbols-outlined" style={{ fontSize: 48, display: 'block', marginBottom: 12, opacity: 0.4 }}>description</span>
              <div style={{ fontSize: 15, marginBottom: 16 }}>No definitions yet</div>
              <button onClick={newDraft} style={{ padding: '10px 20px', borderRadius: 8, border: 'none', background: C.cyan, color: '#021520', fontWeight: 700, cursor: 'pointer' }}>
                Create First Definition
              </button>
            </div>
          )}

          {activeDef && draft && (
            <>
              {/* Definition name */}
              <div style={{ ...glass, borderRadius: 10, padding: '14px 16px' }}>
                <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 6 }}>Definition Name</label>
                <input
                  style={fieldStyle} value={draft.name ?? ''}
                  onChange={e => { setDraft(prev => prev ? { ...prev, name: e.target.value } : prev); setIsDirty(true); }}
                  placeholder="e.g. My Orchestration"
                />
              </div>

              {/* Components */}
              <div style={{ ...glass, borderRadius: 10, padding: '14px 16px' }}>
                <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 10 }}>
                  Components ({draft.components.length})
                </div>
                {draft.components.length === 0 && (
                  <div style={{ fontSize: 12, color: C.textMuted, fontStyle: 'italic' }}>No components. Add from the palette.</div>
                )}
                {draft.components.map(comp => {
                  const hasErrors = !!(errorsByInstance[comp.instance_id]?.length);
                  const isSelected = selectedItem?.id === comp.instance_id;
                  return (
                    <div
                      key={comp.instance_id}
                      style={{
                        padding: '10px 12px', borderRadius: 8, marginBottom: 6,
                        border: `1px solid ${hasErrors ? 'rgba(255,180,171,0.4)' : isSelected ? 'rgba(0,240,255,0.4)' : 'rgba(255,255,255,0.08)'}`,
                        background: isSelected ? 'rgba(0,240,255,0.04)' : 'rgba(255,255,255,0.02)',
                        display: 'flex', alignItems: 'center', gap: 10,
                      }}
                    >
                      <span style={{
                        fontSize: 10, fontWeight: 700, padding: '2px 6px', borderRadius: 4,
                        background: kindColors[comp.definition_ref.kind] ? `${kindColors[comp.definition_ref.kind]}22` : 'rgba(255,255,255,0.08)',
                        color: kindColors[comp.definition_ref.kind] ?? C.textMuted,
                      }}>{comp.definition_ref.kind}</span>
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: 12, color: C.text, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{comp.instance_id}</div>
                        <div style={{ fontSize: 11, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>{comp.definition_ref.namespace}/{comp.definition_ref.name}@{comp.definition_ref.version}</div>
                      </div>
                      {hasErrors && <span className="material-symbols-outlined" style={{ fontSize: 16, color: C.error }} title={(errorsByInstance[comp.instance_id] ?? []).join('; ')}>error</span>}
                      <button onClick={() => setSelectedItem({ type: 'component', id: comp.instance_id })} style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.textMuted, display: 'flex', alignItems: 'center' }}>
                        <span className="material-symbols-outlined" style={{ fontSize: 16 }}>settings</span>
                      </button>
                      <button onClick={() => removeComponent(comp.instance_id)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.error, display: 'flex', alignItems: 'center' }}>
                        <span className="material-symbols-outlined" style={{ fontSize: 16 }}>delete</span>
                      </button>
                    </div>
                  );
                })}
              </div>

              {/* Entry Points */}
              <div style={{ ...glass, borderRadius: 10, padding: '14px 16px' }}>
                <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 10 }}>
                  Entry Points ({draft.entry_points.length})
                </div>
                {draft.entry_points.length === 0 && (
                  <div style={{ fontSize: 12, color: C.textMuted, fontStyle: 'italic' }}>No entry points. Add from the palette.</div>
                )}
                {draft.entry_points.map(ep => {
                  const isSelected = selectedItem?.id === ep.instance_id;
                  return (
                    <div
                      key={ep.instance_id}
                      style={{
                        padding: '10px 12px', borderRadius: 8, marginBottom: 6,
                        border: `1px solid ${isSelected ? C.cyanBorder : 'rgba(0,240,255,0.15)'}`,
                        background: isSelected ? 'rgba(0,240,255,0.04)' : 'rgba(0,240,255,0.02)',
                        display: 'flex', alignItems: 'center', gap: 10,
                      }}
                    >
                      <span style={{ fontSize: 10, fontWeight: 700, padding: '2px 6px', borderRadius: 4, background: 'rgba(0,240,255,0.12)', color: C.cyan }}>{ep.protocol}</span>
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: 12, color: C.text, fontWeight: 600 }}>{ep.slug || ep.instance_id}</div>
                        {ep.root && <div style={{ fontSize: 11, color: C.textMuted }}>→ {ep.root}</div>}
                      </div>
                      <button onClick={() => setSelectedItem({ type: 'ep', id: ep.instance_id })} style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.textMuted, display: 'flex', alignItems: 'center' }}>
                        <span className="material-symbols-outlined" style={{ fontSize: 16 }}>settings</span>
                      </button>
                      <button onClick={() => removeEntryPoint(ep.instance_id)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.error, display: 'flex', alignItems: 'center' }}>
                        <span className="material-symbols-outlined" style={{ fontSize: 16 }}>delete</span>
                      </button>
                    </div>
                  );
                })}
              </div>

              {/* Connections */}
              <div style={{ ...glass, borderRadius: 10, padding: '14px 16px' }}>
                <div style={{ fontSize: 13, fontWeight: 700, color: C.text, marginBottom: 10 }}>
                  Connections ({draft.connections.length})
                </div>
                {draft.connections.length === 0 && (
                  <div style={{ fontSize: 12, color: C.textMuted, fontStyle: 'italic' }}>No explicit connections. Entry→Orchestrator connections derive from entry point root bindings.</div>
                )}
                {draft.connections.map((conn, i) => (
                  <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 10px', borderRadius: 8, marginBottom: 4, border: '1px solid rgba(255,255,255,0.06)', background: 'rgba(255,255,255,0.02)' }}>
                    <span style={{ fontSize: 12, color: C.text, fontFamily: 'JetBrains Mono, monospace' }}>{conn.source}</span>
                    <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted }}>arrow_forward</span>
                    <span style={{ fontSize: 12, color: C.text, fontFamily: 'JetBrains Mono, monospace' }}>{conn.target}</span>
                    <span style={{ marginLeft: 4, fontSize: 10, fontWeight: 700, padding: '1px 5px', borderRadius: 4, background: 'rgba(255,255,255,0.08)', color: C.textMuted }}>{conn.type}</span>
                    <div style={{ flex: 1 }} />
                    <button onClick={() => removeConnection(i)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: C.error, display: 'flex', alignItems: 'center' }}>
                      <span className="material-symbols-outlined" style={{ fontSize: 16 }}>delete</span>
                    </button>
                  </div>
                ))}
              </div>
            </>
          )}
        </div>

        {/* Right panel: Properties (320px) */}
        <div style={{ width: 320, flexShrink: 0, borderLeft: `1px solid ${C.glassBorder}`, overflowY: 'auto', padding: 16, ...glass }}>
          <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, letterSpacing: '0.08em', textTransform: 'uppercase', marginBottom: 12 }}>Properties</div>

          {!selectedItem && (
            <div style={{ fontSize: 12, color: C.textMuted, fontStyle: 'italic' }}>Select a component or entry point to configure.</div>
          )}

          {selectedComp && draft && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <div>
                <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>Instance ID</label>
                <input
                  style={fieldStyle} value={selectedComp.instance_id}
                  onChange={e => updateComponent(selectedComp.instance_id, { instance_id: e.target.value })}
                />
              </div>
              <div>
                <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>Definition</label>
                <div style={{ fontSize: 12, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace', padding: '8px 10px', borderRadius: 6, background: 'rgba(255,255,255,0.04)', border: '1px solid rgba(255,255,255,0.08)' }}>
                  {selectedComp.definition_ref.namespace}/{selectedComp.definition_ref.name}@{selectedComp.definition_ref.version}
                </div>
              </div>
              {selectedComp.definition_ref.kind === 'orchestrator' && (
                <>
                  <div>
                    <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>Name (Temporal key — immutable)</label>
                    <input
                      style={fieldStyle} value={(selectedComp.config as any)?.name ?? selectedComp.name ?? ''}
                      onChange={e => updateComponent(selectedComp.instance_id, { config: { ...selectedComp.config, name: e.target.value } })}
                    />
                  </div>
                  <div>
                    <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>System Prompt</label>
                    <textarea
                      style={{ ...fieldStyle, height: 100, resize: 'vertical' }} value={(selectedComp.config as any)?.system_prompt ?? ''}
                      onChange={e => updateComponent(selectedComp.instance_id, { config: { ...selectedComp.config, system_prompt: e.target.value } })}
                    />
                  </div>
                  <div style={{ fontSize: 11, color: C.textMuted, fontStyle: 'italic', padding: '4px 0' }}>
                    LLM provider &amp; model are configured in App Runtime → LLM Configuration.
                  </div>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
                    {(['max_iterations', 'history_window', 'max_parallel_tools', 'budget_tokens'] as const).map(field => (
                      <div key={field}>
                        <label style={{ fontSize: 10, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.05em', display: 'block', marginBottom: 4 }}>{field.replace(/_/g, ' ')}</label>
                        <input type="number" style={fieldStyle} value={(selectedComp.config as any)?.[field] ?? ''}
                          onChange={e => updateComponent(selectedComp.instance_id, { config: { ...selectedComp.config, [field]: e.target.value === '' ? null : Number(e.target.value) } })} />
                      </div>
                    ))}
                  </div>
                </>
              )}
              {selectedComp.definition_ref.kind !== 'orchestrator' && (
                <div>
                  <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>Config (JSON)</label>
                  <textarea
                    style={{ ...fieldStyle, height: 180, resize: 'vertical', fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}
                    value={JSON.stringify(selectedComp.config, null, 2)}
                    onChange={e => {
                      try { updateComponent(selectedComp.instance_id, { config: JSON.parse(e.target.value) }); } catch { /* invalid JSON — ignore */ }
                    }}
                  />
                </div>
              )}
            </div>
          )}

          {selectedEP && draft && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <div>
                <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>Slug</label>
                <input
                  style={fieldStyle} value={selectedEP.slug}
                  onChange={e => updateEntryPoint(selectedEP.instance_id, { slug: e.target.value })}
                />
              </div>
              <div>
                <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>Protocol</label>
                <select style={fieldStyle} value={selectedEP.protocol}
                  onChange={e => updateEntryPoint(selectedEP.instance_id, { protocol: e.target.value as any })}>
                  {protocolOptions.map(p => <option key={p} value={p}>{p}</option>)}
                </select>
              </div>
              <div>
                <label style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.06em', display: 'block', marginBottom: 4 }}>Root Orchestrator</label>
                <select style={fieldStyle} value={selectedEP.root}
                  onChange={e => updateEntryPoint(selectedEP.instance_id, { root: e.target.value })}>
                  <option value="">— none —</option>
                  {draft.components.filter(c => c.definition_ref.kind === 'orchestrator').map(c => (
                    <option key={c.instance_id} value={c.instance_id}>{c.instance_id}</option>
                  ))}
                </select>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Toast */}
      {toast && (
        <div style={{
          position: 'fixed', bottom: 40, left: '50%', transform: 'translateX(-50%)',
          padding: '10px 20px', borderRadius: 8, fontSize: 13, fontWeight: 600, zIndex: 9999,
          background: toast.ok ? 'rgba(74,222,128,0.15)' : C.errorBg,
          border: `1px solid ${toast.ok ? C.greenBorder : 'rgba(255,180,171,0.3)'}`,
          color: toast.ok ? C.green : C.error,
          boxShadow: '0 4px 20px rgba(0,0,0,0.4)',
          backdropFilter: 'blur(8px)',
        }}>
          {toast.msg}
        </div>
      )}

      {/* Re-publish warning modal */}
      {showRepublishModal && (
        <div style={{
          position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.65)', display: 'flex',
          alignItems: 'center', justifyContent: 'center', zIndex: 10000,
        }}>
          <div style={{
            background: C.surface, border: `1px solid rgba(245,158,11,0.4)`, borderRadius: 14,
            padding: '28px 32px', maxWidth: 420, width: '90%', boxShadow: '0 24px 64px rgba(0,0,0,0.6)',
          }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 14 }}>
              <span className="material-symbols-outlined" style={{ fontSize: 24, color: '#f59e0b' }}>warning</span>
              <span style={{ fontSize: 16, fontWeight: 700, color: C.text }}>Re-publish live app?</span>
            </div>
            <p style={{ fontSize: 13, color: C.textMuted, lineHeight: 1.6, margin: '0 0 22px' }}>
              This app is currently live (revision {app.active_revision}). Re-publishing will apply your changes immediately and may affect active users.
            </p>
            <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end' }}>
              <button
                onClick={() => setShowRepublishModal(false)}
                style={{ padding: '8px 18px', borderRadius: 8, border: '1px solid rgba(255,255,255,0.12)', background: 'rgba(255,255,255,0.06)', color: C.text, cursor: 'pointer', fontSize: 13, fontWeight: 600 }}
              >
                Cancel
              </button>
              <button
                onClick={() => { setShowRepublishModal(false); void publish(); }}
                style={{ padding: '8px 18px', borderRadius: 8, border: '1px solid rgba(245,158,11,0.4)', background: 'rgba(245,158,11,0.15)', color: '#f59e0b', cursor: 'pointer', fontSize: 13, fontWeight: 700 }}
              >
                Re-publish
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
