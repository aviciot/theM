'use client';
import { useState, useRef, useEffect } from 'react';
import type { Proposal, AdvisorMessage } from '../types';
import { PROPOSAL_ALLOWED_FIELDS, FIELD_ICON, FIELD_LABEL } from '../constants';
import { C } from '../constants';

// ── Parsing helpers (only used by AdvisorPanel) ───────────────────────────────
export function parseAdvisorBuffer(buf: string): { text: string; proposals: Proposal[] } {
  const OPEN = '```them-proposal';
  const CLOSE = '```';
  const proposals: Proposal[] = [];
  let text = buf;
  let searchFrom = 0;
  while (true) {
    const openIdx = text.indexOf(OPEN, searchFrom);
    if (openIdx === -1) break;
    const afterOpen = text.indexOf('\n', openIdx);
    if (afterOpen === -1) break;
    const closeIdx = text.indexOf('\n' + CLOSE, afterOpen);
    if (closeIdx === -1) {
      text = text.slice(0, openIdx).trimEnd() + (text.slice(0, openIdx).trim() ? '\n\n_Preparing suggestion…_' : '');
      break;
    }
    const jsonStr = text.slice(afterOpen + 1, closeIdx).trim();
    const blockEnd = closeIdx + 1 + CLOSE.length;
    try {
      const obj = JSON.parse(jsonStr);
      if (obj.id && obj.targetId && obj.targetType && PROPOSAL_ALLOWED_FIELDS.has(obj.field)) {
        proposals.push({
          id: String(obj.id), type: String(obj.type ?? ''),
          targetType: obj.targetType, targetId: String(obj.targetId),
          targetName: String(obj.targetName ?? obj.targetId), field: String(obj.field),
          current: obj.current ?? '', suggested: obj.suggested ?? '',
          reason: String(obj.reason ?? ''), status: 'pending',
        });
      }
    } catch { /* malformed — silently drop */ }
    text = text.slice(0, openIdx).trimEnd() + text.slice(blockEnd);
  }
  return { text, proposals };
}

export function mergeProposals(existing: Proposal[] | undefined, incoming: Proposal[]): Proposal[] {
  if (!existing || existing.length === 0) return incoming;
  const statusMap = new Map(existing.map(p => [p.id, p.status]));
  const errorMap = new Map(existing.map(p => [p.id, p.error]));
  return incoming.map(p => ({
    ...p,
    status: statusMap.get(p.id) ?? p.status,
    error: errorMap.get(p.id),
  }));
}

// ── ProposalCard ──────────────────────────────────────────────────────────────
function ProposalCard({ proposal, msgIndex, onApply }: {
  proposal: Proposal; msgIndex: number; onApply: (msgIndex: number, p: Proposal) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const st = proposal.status;
  const isText = typeof proposal.suggested === 'string' && (proposal.suggested as string).length > 60;

  const btnBg = st === 'applied' ? 'rgba(16,185,129,0.2)'
    : st === 'failed' ? 'rgba(239,68,68,0.2)'
    : st === 'stale' ? 'rgba(251,191,36,0.15)'
    : 'rgba(0,240,255,0.12)';
  const btnColor = st === 'applied' ? '#34d399'
    : st === 'failed' ? '#f87171'
    : st === 'stale' ? '#fbbf24'
    : C.cyan;
  const btnLabel = st === 'applying' ? '…' : st === 'applied' ? 'Applied ✓' : st === 'failed' ? 'Retry' : st === 'stale' ? 'Apply anyway' : 'Apply';

  return (
    <div style={{
      marginTop: 8, borderRadius: 8, border: `1px solid rgba(0,240,255,0.18)`,
      background: 'rgba(0,240,255,0.04)', overflow: 'hidden',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 10px' }}>
        <span className="material-symbols-outlined" style={{ fontSize: 14, color: C.cyan, flexShrink: 0 }}>
          {FIELD_ICON[proposal.field] ?? 'tune'}
        </span>
        <span style={{ fontSize: 11, fontWeight: 700, color: C.cyan, flex: 1 }}>
          {FIELD_LABEL[proposal.field] ?? proposal.field}
          <span style={{ fontWeight: 400, color: C.textMuted }}> · {proposal.targetName}</span>
        </span>
        <button
          onClick={() => setExpanded(e => !e)}
          style={{ border: 'none', background: 'transparent', cursor: 'pointer', color: C.textMuted, padding: 0, lineHeight: 1 }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 14 }}>{expanded ? 'expand_less' : 'expand_more'}</span>
        </button>
      </div>

      <div style={{ padding: '0 10px 7px', fontSize: 11, color: 'var(--tm-card-text-subtle)', lineHeight: 1.5 }}>{proposal.reason}</div>

      {expanded && (
        <div style={{ borderTop: `1px solid rgba(0,240,255,0.1)`, padding: '8px 10px', display: 'flex', flexDirection: 'column', gap: 6 }}>
          <div>
            <div style={{ fontSize: 10, color: C.textMuted, marginBottom: 2 }}>Current</div>
            <div style={{
              fontSize: 11, color: 'var(--tm-card-text-subtle)', background: 'rgba(255,255,255,0.03)', borderRadius: 4,
              padding: '5px 7px', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
              maxHeight: isText ? 80 : 'none', overflowY: isText ? 'auto' : 'visible',
            }}>{String(proposal.current) || '(empty)'}</div>
          </div>
          <div>
            <div style={{ fontSize: 10, color: '#34d399', marginBottom: 2 }}>Suggested</div>
            <div style={{
              fontSize: 11, color: '#d1fae5', background: 'rgba(16,185,129,0.06)', borderRadius: 4,
              padding: '5px 7px', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
              maxHeight: isText ? 120 : 'none', overflowY: isText ? 'auto' : 'visible',
            }}>{String(proposal.suggested)}</div>
          </div>
        </div>
      )}

      <div style={{ padding: '6px 10px 8px', display: 'flex', alignItems: 'center', gap: 8 }}>
        <button
          disabled={st === 'applying' || st === 'applied'}
          onClick={() => onApply(msgIndex, proposal)}
          style={{
            padding: '5px 12px', borderRadius: 6, border: `1px solid ${btnColor}`,
            background: btnBg, color: btnColor, fontSize: 11, fontWeight: 700,
            cursor: st === 'applying' || st === 'applied' ? 'not-allowed' : 'pointer',
            opacity: st === 'applying' ? 0.7 : 1,
          }}
        >{btnLabel}</button>
        {proposal.error && <span style={{ fontSize: 10, color: '#f87171', flex: 1 }}>{proposal.error}</span>}
        {st === 'stale' && !proposal.error && (
          <span style={{ fontSize: 10, color: '#fbbf24' }}>Canvas changed since analysis</span>
        )}
      </div>
    </div>
  );
}

// ── AdvisorPanel ──────────────────────────────────────────────────────────────
export function AdvisorPanel({
  messages, busy, input, scanning,
  onInputChange, onSend, onClose, onRescan,
  onApplyProposal, onApplyAll,
}: {
  messages: AdvisorMessage[];
  busy: boolean;
  input: string;
  scanning: boolean;
  onInputChange: (v: string) => void;
  onSend: (text: string) => void;
  onClose: () => void;
  onRescan: () => void;
  onApplyProposal: (msgIndex: number, p: Proposal) => void;
  onApplyAll: (msgIndex: number) => void;
}) {
  const bottomRef = useRef<HTMLDivElement>(null);
  useEffect(() => { bottomRef.current?.scrollIntoView({ behavior: 'smooth' }); }, [messages]);

  return (
    <div style={{
      width: 380, flexShrink: 0, height: '100%', display: 'flex', flexDirection: 'column',
      background: 'var(--tm-card-chrome)', borderLeft: `1px solid rgba(0,240,255,0.15)`,
      boxShadow: '-4px 0 24px rgba(0,0,0,0.4)',
    }}>
      {/* Header */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 8, padding: '11px 14px',
        borderBottom: `1px solid ${C.glassBorder}`, flexShrink: 0,
      }}>
        <span className="material-symbols-outlined" style={{ fontSize: 17, color: C.cyan }}>assistant</span>
        <span style={{ fontWeight: 700, fontSize: 13, color: C.text, flex: 1 }}>AI Workflow Advisor</span>
        {scanning && (
          <span style={{ fontSize: 11, color: C.cyan, fontStyle: 'italic' }}>Scanning…</span>
        )}
        <button
          onClick={onRescan}
          title="Re-analyze workflow"
          disabled={busy || scanning}
          style={{ width: 26, height: 26, borderRadius: 5, border: 'none', background: 'transparent',
            color: busy || scanning ? C.outlineVariant : C.textMuted, cursor: busy || scanning ? 'not-allowed' : 'pointer',
            display: 'flex', alignItems: 'center', justifyContent: 'center' }}
          onMouseEnter={e => { if (!busy && !scanning) e.currentTarget.style.color = C.cyan; }}
          onMouseLeave={e => { e.currentTarget.style.color = C.textMuted; }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 15 }}>refresh</span>
        </button>
        <button
          onClick={onClose}
          style={{ width: 26, height: 26, borderRadius: 5, border: 'none', background: 'transparent',
            color: C.textMuted, cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center' }}
          onMouseEnter={e => (e.currentTarget.style.color = C.text)}
          onMouseLeave={e => (e.currentTarget.style.color = C.textMuted)}
        >
          <span className="material-symbols-outlined" style={{ fontSize: 15 }}>close</span>
        </button>
      </div>

      {/* Messages */}
      <div style={{ flex: 1, overflowY: 'auto', padding: '14px 14px', display: 'flex', flexDirection: 'column', gap: 10 }}>
        {messages.length === 0 && !busy && !scanning && (
          <div style={{ fontSize: 13, color: C.textMuted, fontStyle: 'italic', textAlign: 'center', marginTop: 40 }}>
            Scanning your workflow…
          </div>
        )}
        {messages.map((m, i) => {
          const pendingCount = (m.proposals ?? []).filter(p => p.status === 'pending' || p.status === 'stale').length;
          return (
            <div key={i} style={{ display: 'flex', flexDirection: 'column', alignItems: m.role === 'user' ? 'flex-end' : 'flex-start' }}>
              {m.role === 'assistant' && (
                <span style={{ fontSize: 10, color: C.textMuted, marginBottom: 3, paddingLeft: 2 }}>AI Advisor</span>
              )}
              <div style={{
                maxWidth: '96%', padding: '9px 12px',
                borderRadius: m.role === 'user' ? '12px 12px 2px 12px' : '2px 12px 12px 12px',
                background: m.role === 'user' ? 'rgba(0,240,255,0.08)' : 'rgba(255,255,255,0.04)',
                border: `1px solid ${m.role === 'user' ? 'rgba(0,240,255,0.2)' : C.outlineVariant}`,
                fontSize: 13, color: m.role === 'user' ? C.text : 'var(--tm-card-text-hint)',
                lineHeight: 1.65, whiteSpace: 'pre-wrap', wordBreak: 'break-word',
              }}>
                {m.text}
                {m.streaming && <span style={{ opacity: 0.6, marginLeft: 2 }}>▋</span>}
              </div>
              {(m.proposals ?? []).length > 0 && (
                <div style={{ width: '96%', display: 'flex', flexDirection: 'column', gap: 0 }}>
                  {m.proposals!.map(p => (
                    <ProposalCard key={`${i}-${p.id}`} proposal={p} msgIndex={i} onApply={onApplyProposal} />
                  ))}
                  {pendingCount >= 2 && (
                    <button
                      onClick={() => onApplyAll(i)}
                      style={{
                        marginTop: 8, padding: '6px 0', borderRadius: 7,
                        border: `1px solid rgba(0,240,255,0.3)`,
                        background: 'rgba(0,240,255,0.08)', color: C.cyan,
                        fontSize: 11, fontWeight: 700, cursor: 'pointer', width: '100%',
                      }}
                    >Apply all ({pendingCount})</button>
                  )}
                </div>
              )}
            </div>
          );
        })}
        {busy && messages[messages.length - 1]?.role !== 'assistant' && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, paddingLeft: 2 }}>
            <span style={{ fontSize: 11, color: C.cyan, fontStyle: 'italic' }}>Thinking…</span>
          </div>
        )}
        <div ref={bottomRef} />
      </div>

      {/* Input */}
      <div style={{ padding: '10px 14px', borderTop: `1px solid ${C.glassBorder}`, flexShrink: 0 }}>
        <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end' }}>
          <textarea
            value={input}
            onChange={e => onInputChange(e.target.value)}
            onKeyDown={e => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                if (!busy && !scanning && input.trim()) { onSend(input.trim()); onInputChange(''); }
              }
            }}
            placeholder="Ask a follow-up question…"
            disabled={busy || scanning}
            rows={2}
            style={{
              flex: 1, background: 'rgba(255,255,255,0.04)', border: `1px solid ${C.outlineVariant}`,
              borderRadius: 8, color: 'var(--tm-card-text)', fontSize: 13, padding: '7px 10px',
              resize: 'none', outline: 'none', fontFamily: 'inherit',
              opacity: (busy || scanning) ? 0.5 : 1,
            }}
          />
          <button
            onClick={() => { if (!busy && !scanning && input.trim()) { onSend(input.trim()); onInputChange(''); } }}
            disabled={busy || scanning || !input.trim()}
            style={{
              padding: '8px 12px', borderRadius: 8, border: 'none', flexShrink: 0,
              background: (!busy && !scanning && input.trim()) ? C.cyan : C.outlineVariant,
              color: (!busy && !scanning && input.trim()) ? '#00363a' : C.textMuted,
              cursor: (!busy && !scanning && input.trim()) ? 'pointer' : 'not-allowed',
              fontWeight: 700, fontSize: 12,
            }}
          >
            Send
          </button>
        </div>
        <div style={{ fontSize: 11, color: C.textMuted, marginTop: 5, paddingLeft: 2 }}>
          Shift+Enter for newline · Enter to send
        </div>
      </div>
    </div>
  );
}
