'use client';
import type { Node } from '@xyflow/react';
import type { Application, AppDefinition, ValidationReport } from '@/lib/api';
import { C } from '../constants';

// ── CanvasTopBar ──────────────────────────────────────────────────────────────
// Extracted from CanvasBuilderView.tsx (which was over the 400-line guideline)
// per docs/APP_CANVAS_EXPORT_IMPORT_PLAN.md's file-size cleanup, done ahead of
// adding Export/Import JSON buttons here. Pure presentation — all state and
// handlers are owned by CanvasBuilderView and passed down as props.

export function CanvasTopBar({
  app,
  activeDef,
  isLive,
  isDirty,
  saving,
  publishing,
  validating,
  validationReport,
  executionBackend,
  nodes,
  onBack,
  onSetExecutionBackend,
  onSaveDraft,
  onPublishClick,
  exportButton,
  importControls,
}: {
  app: Application;
  activeDef: AppDefinition | null;
  isLive: boolean;
  isDirty: boolean;
  saving: boolean;
  publishing: boolean;
  validating: boolean;
  validationReport: ValidationReport | null;
  executionBackend: 'local' | 'temporal' | undefined;
  nodes: Node[];
  onBack: () => void;
  onSetExecutionBackend: (v: 'local' | 'temporal') => void;
  onSaveDraft: () => void;
  onPublishClick: () => void;
  /** Slot for the Export JSON button (docs/APP_CANVAS_EXPORT_IMPORT_PLAN.md) — kept as an
   * injected node rather than owned here so CanvasTopBar stays a pure display component. */
  exportButton?: React.ReactNode;
  /** Slot for the Import JSON button + hidden file input. */
  importControls?: React.ReactNode;
}) {
  return (
    <>
      <div style={{
        height: 56, flexShrink: 0, display: 'flex', alignItems: 'center', gap: 10,
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
        {importControls}
        {exportButton}
        {activeDef && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '4px 10px', borderRadius: 8, background: 'rgba(255,255,255,0.04)', border: '1px solid rgba(255,255,255,0.08)' }}>
            <span style={{ fontSize: 11, color: C.textMuted, fontWeight: 600 }}>Execution</span>
            <select
              value={executionBackend ?? 'local'}
              onChange={e => onSetExecutionBackend(e.target.value as 'local' | 'temporal')}
              style={{ background: 'rgba(0,0,0,0.3)', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 6, color: C.text, fontSize: 11, padding: '3px 6px', cursor: 'pointer', outline: 'none' }}
            >
              <option value="local">Simple (Orchestrator)</option>
              <option value="temporal">Graph (Canvas Flow)</option>
            </select>
          </div>
        )}
        {activeDef && isDirty && (
          <button onClick={onSaveDraft} disabled={saving} style={{ padding: '7px 14px', borderRadius: 8, border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 700, background: 'rgba(255,255,255,0.08)', color: C.text }}>
            {saving ? 'Saving…' : 'Save'}
          </button>
        )}
        {activeDef && (
          <button
            onClick={onPublishClick}
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
        <div style={{ background: C.errorBg, borderBottom: `1px solid rgba(255,180,171,0.3)`, padding: '10px 20px', flexShrink: 0 }}>
          <div style={{ fontSize: 12, fontWeight: 700, color: C.error, marginBottom: 4 }}>Validation errors:</div>
          {(validationReport.errors ?? []).map((err, i) => (
            <div key={i} style={{ fontSize: 12, color: C.error }}>
              {err.instance_id ? `[${err.instance_id}] ` : ''}{err.message}
            </div>
          ))}
        </div>
      )}

      {/* Backend-mismatch warning: inline/flow-control nodes are Temporal-only, advisory only */}
      {executionBackend !== 'temporal' && nodes.some(n => n.type === 'inline' || n.type === 'flowControl') && (
        <div style={{ background: C.amberBg, borderBottom: `1px solid ${C.amberBorder}`, padding: '10px 20px', flexShrink: 0 }}>
          <div style={{ fontSize: 12, color: C.amber }}>
            Inline and flow-control nodes only execute in Graph mode. This app is set to
            Simple (Orchestrator) — the canvas graph is not executed. Switch Execution to
            &quot;Graph (Canvas Flow)&quot; to run this flow.
          </div>
        </div>
      )}
    </>
  );
}
