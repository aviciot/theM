'use client';
import { useState, useEffect } from 'react';
import type { Node, Edge } from '@xyflow/react';
import type { MwNodeData } from '../../../types';
import { C } from '../../../constants';
import type { MiddlewareWiring } from '@/lib/api';
import { themApi } from '@/lib/api';
import { fieldStyle, selectStyle, chipStyle } from './panelShared';

// ── MiddlewareNodePanel (File Guard) ─────────────────────────────────────────

interface Props {
  appId: string;
  selectedNode: Node;
  nodes: Node[];
  edges: Edge[];
  setNodes: (updater: (ns: Node[]) => Node[]) => void;
  showToast: (msg: string, ok: boolean) => void;
}

export function MiddlewareNodePanel({
  appId, selectedNode, nodes, edges, setNodes, showToast,
}: Props) {
  const liveMwNode = nodes.find(n => n.id === selectedNode.id);
  const d = (liveMwNode?.data ?? selectedNode.data) as unknown as MwNodeData;

  // ── File Guard wiring state ────────────────────────────────────────────────
  const [existingWiring, setExistingWiring] = useState<MiddlewareWiring | null>(null);
  const [wiringLoading, setWiringLoading] = useState(false);
  const [wiringSaving, setWiringSaving] = useState(false);
  const [guardEnabled, setGuardEnabled] = useState(false);
  const [guardMode, setGuardMode] = useState<'block' | 'warn'>('block');
  const [guardMaxMb, setGuardMaxMb] = useState(5);
  const [guardAllowedTypes, setGuardAllowedTypes] = useState('');
  const [guardBlockedTypes, setGuardBlockedTypes] = useState('exe,sh,bat,ps1,cmd');
  const [guardNotify, setGuardNotify] = useState(true);

  // Find the agent node connected to a middleware node (middleware → agent edge).
  function connectedAgentId(mwNodeId: string): string {
    const edge = edges.find(e => e.source === mwNodeId);
    if (!edge) return '';
    const agentNode = nodes.find(n => n.id === edge.target && n.type === 'agent');
    if (!agentNode) return '';
    return (agentNode.data as unknown as { definition_id?: string }).definition_id ?? '';
  }

  // Load wiring when a middleware node is selected.
  useEffect(() => {
    if (!selectedNode || selectedNode.type !== 'middleware') return;
    const agentId = connectedAgentId(selectedNode.id);
    if (!appId || !agentId) { setExistingWiring(null); return; }
    setWiringLoading(true);
    themApi.listMiddlewareWirings(appId)
      .then(wirings => {
        const w = wirings.find(w => w.agent_id === agentId && w.def_slug === 'file-guard') ?? null;
        setExistingWiring(w);
        if (w) {
          const cfg = w.config_override as Record<string, unknown>;
          setGuardEnabled(w.enabled);
          setGuardMode((cfg.mode as 'block' | 'warn') ?? 'block');
          setGuardMaxMb((cfg.max_file_size_mb as number) ?? 5);
          setGuardAllowedTypes(((cfg.allowed_types as string[]) ?? []).join(','));
          setGuardBlockedTypes(((cfg.blocked_types as string[]) ?? ['exe','sh','bat','ps1','cmd']).join(','));
          setGuardNotify((cfg.notify_on_fail as boolean) ?? true);
          if (selectedNode) {
            setNodes(ns => ns.map(n => n.id === selectedNode.id ? { ...n, data: { ...n.data, wiringEnabled: w.enabled } } : n));
          }
        } else {
          setGuardEnabled(false); setGuardMode('block'); setGuardMaxMb(5);
          setGuardAllowedTypes(''); setGuardBlockedTypes('exe,sh,bat,ps1,cmd'); setGuardNotify(true);
          if (selectedNode) {
            setNodes(ns => ns.map(n => n.id === selectedNode.id ? { ...n, data: { ...n.data, wiringEnabled: false } } : n));
          }
        }
      })
      .catch(() => setExistingWiring(null))
      .finally(() => setWiringLoading(false));
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedNode?.id, appId]);

  async function saveGuardWiring() {
    if (!selectedNode) return;
    const agentId = connectedAgentId(selectedNode.id);
    if (!agentId) { showToast('Connect this guard to an agent first', false); return; }
    const cfgOverride = {
      enabled: guardEnabled,
      mode: guardMode,
      max_file_size_mb: guardMaxMb,
      allowed_types: guardAllowedTypes.split(',').map(s => s.trim()).filter(Boolean),
      blocked_types: guardBlockedTypes.split(',').map(s => s.trim()).filter(Boolean),
      notify_on_fail: guardNotify,
    };
    setWiringSaving(true);
    try {
      if (existingWiring) {
        const updated = await themApi.updateMiddlewareWiring(appId, existingWiring.id, {
          enabled: guardEnabled,
          config_override: cfgOverride,
          node_id: selectedNode.id,
        });
        setExistingWiring(updated);
      } else {
        const created = await themApi.createMiddlewareWiring(appId, {
          agent_id: agentId,
          def_slug: 'file-guard',
          enabled: guardEnabled,
          config_override: cfgOverride,
          node_id: selectedNode.id,
        });
        setExistingWiring(created);
      }
      setNodes(ns => ns.map(n => n.id === selectedNode.id ? { ...n, data: { ...n.data, wiringEnabled: guardEnabled } } : n));
      showToast('File Guard saved', true);
    } catch {
      showToast('Failed to save File Guard', false);
    } finally {
      setWiringSaving(false);
    }
  }

  async function deleteGuardWiring() {
    if (!existingWiring || !selectedNode) return;
    setWiringSaving(true);
    try {
      await themApi.deleteMiddlewareWiring(appId, existingWiring.id);
      setExistingWiring(null);
      setGuardEnabled(false);
      setNodes(ns => ns.map(n => n.id === selectedNode.id ? { ...n, data: { ...n.data, wiringEnabled: false } } : n));
      showToast('File Guard removed', true);
    } catch {
      showToast('Failed to remove File Guard', false);
    } finally {
      setWiringSaving(false);
    }
  }

  const agentId = connectedAgentId(selectedNode.id);
  const toggleStyle: React.CSSProperties = {
    position: 'relative', display: 'inline-block', width: 36, height: 20, cursor: 'pointer', flexShrink: 0,
  };
  const toggleTrack = (on: boolean): React.CSSProperties => ({
    position: 'absolute', inset: 0, borderRadius: 10,
    background: on ? C.amber : 'rgba(255,255,255,0.12)', transition: 'background 0.2s',
  });
  const toggleThumb = (on: boolean): React.CSSProperties => ({
    position: 'absolute', top: 2, left: on ? 18 : 2, width: 16, height: 16,
    borderRadius: '50%', background: '#fff', transition: 'left 0.2s',
  });
  return (
    <div style={{ padding: 16, display: 'flex', flexDirection: 'column', gap: 14, overflowY: 'auto' }}>
      <div style={{ fontSize: 12, fontWeight: 700, color: C.amber, textTransform: 'uppercase', letterSpacing: '0.06em' }}>File Guard</div>

      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <div style={chipStyle}>{d.instance_id}</div>
        <div style={{ fontSize: 12, color: C.textMuted }}>{d.display_name}</div>
      </div>

      {!agentId && (
        <div style={{ fontSize: 12, color: C.amber, background: 'rgba(251,191,36,0.08)', borderRadius: 8, padding: '8px 12px' }}>
          Connect this guard to an A2A agent to activate scanning
        </div>
      )}

      {wiringLoading && <div style={{ fontSize: 12, color: C.textMuted }}>Loading…</div>}

      {!wiringLoading && (
        <>
          {/* Enabled toggle */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <label style={{ fontSize: 13, color: C.text }}>Enabled</label>
            <div style={toggleStyle} onClick={() => setGuardEnabled(v => !v)}>
              <div style={toggleTrack(guardEnabled)} />
              <div style={toggleThumb(guardEnabled)} />
            </div>
          </div>

          {/* Mode */}
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Mode</label>
            <select style={selectStyle} value={guardMode} onChange={e => setGuardMode(e.target.value as 'block' | 'warn')}>
              <option value="block">Block — infected files are quarantined</option>
              <option value="warn">Warn — log only, files still delivered</option>
            </select>
          </div>

          {/* Max file size */}
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>Max file size (MB)</label>
            <input type="number" min={1} max={500} style={fieldStyle}
              value={guardMaxMb} onChange={e => setGuardMaxMb(Number(e.target.value) || 5)} />
          </div>

          {/* Allowed types */}
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>
              Allowed types <span style={{ fontWeight: 400 }}>(comma-separated, empty = all)</span>
            </label>
            <input style={fieldStyle} placeholder="pdf, png, csv"
              value={guardAllowedTypes} onChange={e => setGuardAllowedTypes(e.target.value)} />
          </div>

          {/* Blocked types */}
          <div>
            <label style={{ fontSize: 11, color: C.textMuted, display: 'block', marginBottom: 4 }}>
              Blocked types <span style={{ fontWeight: 400 }}>(comma-separated)</span>
            </label>
            <input style={fieldStyle} placeholder="exe, sh, bat"
              value={guardBlockedTypes} onChange={e => setGuardBlockedTypes(e.target.value)} />
          </div>

          {/* Notify on fail */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <label style={{ fontSize: 13, color: C.text }}>Notify on blocked file</label>
            <div style={toggleStyle} onClick={() => setGuardNotify(v => !v)}>
              <div style={toggleTrack(guardNotify)} />
              <div style={toggleThumb(guardNotify)} />
            </div>
          </div>

          {/* Status indicator */}
          {existingWiring && (
            <div style={{ fontSize: 11, color: C.textMuted }}>
              Wiring active · agent: <span style={{ fontFamily: 'JetBrains Mono, monospace' }}>{existingWiring.agent_slug}</span>
            </div>
          )}

          {/* Actions */}
          <div style={{ display: 'flex', gap: 8, marginTop: 4 }}>
            <button
              disabled={wiringSaving || !agentId}
              onClick={saveGuardWiring}
              style={{ flex: 1, padding: '8px 0', borderRadius: 8, border: 'none', cursor: agentId ? 'pointer' : 'not-allowed',
                background: agentId ? C.amber : 'rgba(255,255,255,0.08)', color: agentId ? '#000' : C.textMuted, fontWeight: 600, fontSize: 13 }}
            >
              {wiringSaving ? 'Saving…' : existingWiring ? 'Update Guard' : 'Save Guard'}
            </button>
            {existingWiring && (
              <button
                disabled={wiringSaving}
                onClick={deleteGuardWiring}
                style={{ padding: '8px 14px', borderRadius: 8, border: 'none', cursor: 'pointer',
                  background: 'rgba(239,68,68,0.15)', color: '#f87171', fontWeight: 600, fontSize: 13 }}
              >
                Remove
              </button>
            )}
          </div>
        </>
      )}
    </div>
  );
}
