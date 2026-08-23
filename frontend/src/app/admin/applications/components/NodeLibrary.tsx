'use client';
import { useState, useRef, type DragEvent } from 'react';
import type { Agent, MiddlewareDef } from '../types';
import { C, EP_META, glass } from '../constants';
import { agentIconForLibrary, trunc } from './CanvasHelpers';
import type { MiddlewareData } from '../types';

export function NodeLibrary({ agents, middlewareDefs, width, onWidthChange }: {
  agents: Agent[];
  middlewareDefs: MiddlewareDef[];
  width: number;
  onWidthChange: (w: number) => void;
}) {
  const [openEP, setOpenEP] = useState(true);
  const [openOrch, setOpenOrch] = useState(true);
  const [openAgents, setOpenAgents] = useState(true);
  const [openMW, setOpenMW] = useState(true);
  const dragging = useRef(false);
  const startX = useRef(0);
  const startW = useRef(0);

  function onResizeMouseDown(e: React.MouseEvent) {
    e.preventDefault();
    dragging.current = true;
    startX.current = e.clientX;
    startW.current = width;
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';

    function onMove(ev: MouseEvent) {
      if (!dragging.current) return;
      const delta = ev.clientX - startX.current;
      const next = Math.min(480, Math.max(200, startW.current + delta));
      onWidthChange(next);
    }
    function onUp() {
      dragging.current = false;
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    }
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }

  function dragItem(e: DragEvent, nodeType: string, nodeData: object) {
    e.dataTransfer.setData('nodeType', nodeType);
    e.dataTransfer.setData('nodeData', JSON.stringify(nodeData));
    e.dataTransfer.effectAllowed = 'move';
  }

  const itemStyle: React.CSSProperties = {
    display: 'flex', alignItems: 'center', gap: 10, padding: '9px 12px',
    borderRadius: 8, cursor: 'grab', userSelect: 'none',
    border: `1px solid transparent`, transition: 'all 0.15s', marginBottom: 4,
  };

  function SectionHeader({ label, open, onToggle }: { label: string; open: boolean; onToggle: () => void }) {
    return (
      <button onClick={onToggle} style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        width: '100%', padding: '6px 0', background: 'none', border: 'none', cursor: 'pointer',
        fontSize: 10, fontWeight: 700, color: C.textMuted, letterSpacing: 1.5, textTransform: 'uppercase',
        marginBottom: open ? 8 : 0,
      }}>
        {label}
        <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted, transition: 'transform 0.15s', transform: open ? 'rotate(180deg)' : 'none' }}>expand_more</span>
      </button>
    );
  }

  return (
    <div style={{ display: 'flex', height: '100%', flexShrink: 0 }}>
      {/* Panel body */}
      <div style={{
        width, height: '100%', overflowY: 'auto',
        ...glass, borderRight: 'none', padding: '16px 14px',
        display: 'flex', flexDirection: 'column', gap: 20,
      }}>
        <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, letterSpacing: 1, textTransform: 'uppercase', paddingBottom: 8, borderBottom: `1px solid ${C.outlineVariant}` }}>
          Node Library
        </div>

        {/* Entry Points */}
        <div>
          <SectionHeader label="Entry Points" open={openEP} onToggle={() => setOpenEP(v => !v)} />
          {openEP && (
            <div className="nl-section-list">
              {(['websocket', 'sse', 'webrtc', 'a2a', 'voice'] as const).map(ep => {
                const meta = EP_META[ep];
                const isAmber = ep === 'a2a' || ep === 'voice';
                return (
                  <div key={ep} className="nl-tooltip" style={{ position: 'relative', marginBottom: 4 }}>
                    <div
                      draggable
                      onDragStart={e => dragItem(e, 'entryPoint', { epType: ep, label: meta.title, accessMode: 'token', slug: '' })}
                      style={{ ...itemStyle, background: isAmber ? C.amberBg : C.cyanBg, borderColor: isAmber ? C.amberBorder : C.cyanBorder, marginBottom: 0 }}
                      onMouseEnter={e => (e.currentTarget.style.background = isAmber ? 'rgba(245,158,11,0.1)' : 'rgba(0,240,255,0.1)')}
                      onMouseLeave={e => (e.currentTarget.style.background = isAmber ? C.amberBg : C.cyanBg)}
                    >
                      <span style={{ fontSize: 20, lineHeight: 1, flexShrink: 0 }}>{meta.emoji}</span>
                      <div style={{ minWidth: 0 }}>
                        <div style={{ fontSize: 13, fontWeight: 600, color: C.text }}>{meta.title}</div>
                        <div style={{ fontSize: 10, color: C.textMuted }}>Entry point</div>
                      </div>
                      <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted, marginLeft: 'auto', opacity: 0.5 }}>drag_indicator</span>
                    </div>
                    <div className="nl-tip">
                      <div style={{ fontSize: 11, fontWeight: 700, color: C.cyan, marginBottom: 4 }}>{meta.title}</div>
                      <div style={{ fontSize: 11, color: 'var(--tm-card-text-hint)', lineHeight: 1.5 }}>{meta.desc}</div>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* Orchestrators */}
        <div>
          <SectionHeader label="Orchestrators" open={openOrch} onToggle={() => setOpenOrch(v => !v)} />
          {openOrch && (
            <div className="nl-section-list">
              <div className="nl-tooltip" style={{ position: 'relative', marginBottom: 4 }}>
                <div
                  draggable
                  onDragStart={e => dragItem(e, 'orchestrator', {
                    orchestratorId: null, appOrchestratorId: null,
                    name: '', displayName: '',
                    systemPrompt: '', allowedAgentIds: [],
                    llmProvider: '', llmModel: '', llmApiKey: '',
                    maxIterations: null, historyWindow: null,
                    maxParallelTools: null,
                    delegatable: false, kind: 'standard', budgetTokens: null,
                    transcriptionProvider: null, transcriptionModel: null, transcriptionApiKey: null,
                    ttsProvider: null, ttsVoice: null, ttsApiKey: null,
                  })}
                  style={{ ...itemStyle, background: C.purpleBg, borderColor: 'rgba(208,188,255,0.2)', marginBottom: 0 }}
                  onMouseEnter={e => (e.currentTarget.style.background = 'rgba(87,27,193,0.2)')}
                  onMouseLeave={e => (e.currentTarget.style.background = C.purpleBg)}
                >
                  <span className="material-symbols-outlined" style={{ fontSize: 18, color: C.purple, flexShrink: 0 }}>hub</span>
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontSize: 13, fontWeight: 600, color: C.text }}>Orchestrator</div>
                    <div style={{ fontSize: 10, color: C.textMuted, fontFamily: 'JetBrains Mono, monospace' }}>claude-sonnet-4-6</div>
                  </div>
                  <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted, marginLeft: 'auto', flexShrink: 0, opacity: 0.5 }}>drag_indicator</span>
                </div>
                <div className="nl-tip">
                  <div style={{ fontSize: 11, fontWeight: 700, color: C.purple, marginBottom: 4 }}>Orchestrator</div>
                  <div style={{ fontSize: 11, color: 'var(--tm-card-text-hint)', lineHeight: 1.5 }}>Drop onto canvas to create a new orchestrator instance. Configure model, system prompt, and agents in the inspector.</div>
                </div>
              </div>
            </div>
          )}
        </div>

        {/* Agents */}
        <div>
          <SectionHeader label="Agents" open={openAgents} onToggle={() => setOpenAgents(v => !v)} />
          {openAgents && (
            <div className="nl-section-list">
              {agents.filter(a => a.enabled && !a.tags?.includes('internal')).map(a => {
                const icon = a.icon || agentIconForLibrary(a);
                return (
                  <div key={a.id} className="nl-tooltip" style={{ position: 'relative', marginBottom: 4 }}>
                    <div
                      draggable
                      onDragStart={e => dragItem(e, 'agent', { agentId: a.id, name: a.slug, displayName: a.display_name, description: a.description, transport: a.transport, endpointUrl: a.endpoint_url, icon: a.icon || agentIconForLibrary(a) })}
                      style={{ ...itemStyle, background: C.greenBg, borderColor: C.greenBorder, marginBottom: 0 }}
                      onMouseEnter={e => (e.currentTarget.style.background = 'rgba(74,222,128,0.1)')}
                      onMouseLeave={e => (e.currentTarget.style.background = C.greenBg)}
                    >
                      <span className="material-symbols-outlined" style={{ fontSize: 18, color: C.green, flexShrink: 0 }}>{icon}</span>
                      <div style={{ minWidth: 0 }}>
                        <div style={{ fontSize: 13, fontWeight: 600, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.display_name}</div>
                        <div style={{ fontSize: 10, color: C.textMuted, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.transport}</div>
                      </div>
                      <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted, marginLeft: 'auto', flexShrink: 0, opacity: 0.5 }}>drag_indicator</span>
                    </div>
                    <div className="nl-tip">
                      <div style={{ fontSize: 11, fontWeight: 700, color: C.green, marginBottom: 4 }}>{a.display_name}</div>
                      <div style={{ fontSize: 10, color: C.textMuted, marginBottom: 6 }}>{a.transport} · {a.slug}</div>
                      <div style={{ fontSize: 11, color: 'var(--tm-card-text-hint)', lineHeight: 1.5 }}>{trunc(a.description)}</div>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* Middleware */}
        {middlewareDefs.length > 0 && (
          <div>
            <SectionHeader label="Middleware" open={openMW} onToggle={() => setOpenMW(v => !v)} />
            {openMW && (
              <div className="nl-section-list">
                {middlewareDefs.filter(m => m.enabled).map(m => {
                  const icon = m.kind === 'guard' ? 'shield' : 'bolt';
                  return (
                    <div key={m.id} className="nl-tooltip" style={{ position: 'relative', marginBottom: 4 }}>
                      <div
                        draggable
                        onDragStart={e => dragItem(e, 'middleware', {
                          defId: m.id, slug: m.slug, kind: m.kind,
                          displayName: m.display_name, description: m.description,
                          config: m.config, configOverride: {}, nodeId: '',
                        } satisfies MiddlewareData)}
                        style={{ ...itemStyle, background: C.amberBg, borderColor: C.amberBorder, marginBottom: 0 }}
                        onMouseEnter={e => (e.currentTarget.style.background = 'rgba(245,158,11,0.1)')}
                        onMouseLeave={e => (e.currentTarget.style.background = C.amberBg)}
                      >
                        <span className="material-symbols-outlined" style={{ fontSize: 18, color: C.amber, flexShrink: 0 }}>{icon}</span>
                        <div style={{ minWidth: 0 }}>
                          <div style={{ fontSize: 13, fontWeight: 600, color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{m.display_name}</div>
                          <div style={{ fontSize: 10, color: C.textMuted, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{m.kind}</div>
                        </div>
                        <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.textMuted, marginLeft: 'auto', flexShrink: 0, opacity: 0.5 }}>drag_indicator</span>
                      </div>
                      <div className="nl-tip">
                        <div style={{ fontSize: 11, fontWeight: 700, color: C.amber, marginBottom: 4 }}>{m.display_name}</div>
                        <div style={{ fontSize: 10, color: C.textMuted, marginBottom: 6 }}>{m.kind} · {m.slug}</div>
                        <div style={{ fontSize: 11, color: 'var(--tm-card-text-hint)', lineHeight: 1.5 }}>{trunc(m.description)}</div>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        )}
      </div>

      {/* Resize handle */}
      <div
        className="nl-resize-handle"
        onMouseDown={onResizeMouseDown}
        style={{ borderRight: `1px solid ${C.glassBorder}` }}
      />
    </div>
  );
}
