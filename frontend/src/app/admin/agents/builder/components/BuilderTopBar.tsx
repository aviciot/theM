import React from 'react';
import { C } from '../constants';

interface BuilderTopBarProps {
  activeView: 'agent' | 'skill';
  activeSkillId: string | null;
  defId: string | null;
  agentSlug: string;
  onSlugChange: (v: string) => void;
  dirty: boolean;
  saving: boolean;
  deleting: boolean;
  validating: boolean;
  publishing: boolean;
  saveError: string;
  publishedRevision: number | null;
  errorCount: number;
  warningCount: number;
  validationLoading: boolean;
  lastValidatedAt: number | null;
  debugActive: boolean;
  canUndo: boolean;
  canRedo: boolean;
  importFileRef: React.RefObject<HTMLInputElement | null>;
  onBack: () => void;
  onSave: () => void;
  onDelete: () => void;
  onValidate: () => void;
  onPublish: () => void;
  onExport: () => void;
  onImportJSON: () => void;
  onImportFileChange: (e: React.ChangeEvent<HTMLInputElement>) => void;
  onUndo: () => void;
  onRedo: () => void;
  onDebugToggle: () => void;
}

export function BuilderTopBar({
  activeView, activeSkillId, defId, agentSlug, onSlugChange,
  dirty, saving, deleting, validating, publishing,
  saveError, publishedRevision, errorCount, warningCount,
  validationLoading, lastValidatedAt, debugActive,
  canUndo, canRedo, importFileRef,
  onBack, onSave, onDelete, onValidate, onPublish, onExport,
  onImportJSON, onImportFileChange, onUndo, onRedo, onDebugToggle,
}: BuilderTopBarProps) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: '12px',
      padding: '12px 24px', borderBottom: `1px solid ${C.outline}`,
      background: C.surface, flexShrink: 0,
    }}>
      <button onClick={onBack} style={{
        background: 'transparent', border: `1px solid ${C.outline}`, color: C.textMuted,
        padding: '6px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
      }}>
        {activeView === 'skill' ? 'Back to Agent' : 'Back to Agents'}
      </button>

      <div style={{ flex: 1 }}>
        {activeView === 'agent' ? (
          <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
            <input
              value={agentSlug}
              onChange={e => onSlugChange(e.target.value)}
              placeholder="agent-slug (kebab-case)"
              style={{
                background: 'transparent', border: `1px solid ${C.outline}`, color: '#fff',
                padding: '6px 12px', borderRadius: '6px', fontSize: '13px', width: '220px',
              }}
            />
            <span style={{ color: C.textMuted, fontSize: '12px' }}>Agent Builder</span>
          </div>
        ) : (
          <span style={{ color: C.purple, fontWeight: 600, fontSize: '14px' }}>
            Pipeline: {activeSkillId}
          </span>
        )}
      </div>

      {saveError && (
        <span style={{ color: '#f87171', fontSize: '12px', maxWidth: '300px' }}>{saveError}</span>
      )}
      {publishedRevision !== null && (
        <span style={{ color: '#34d399', fontSize: '12px' }}>Published rev {publishedRevision}</span>
      )}

      {defId && (validationLoading || errorCount > 0 || warningCount > 0) && (
        <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
          {validationLoading && (
            <span style={{ color: '#64748b', fontSize: '11px', fontStyle: 'italic' }}>validating…</span>
          )}
          {!validationLoading && errorCount > 0 && (
            <span style={{
              background: 'rgba(248,113,113,0.15)', border: '1px solid rgba(248,113,113,0.4)',
              color: '#f87171', padding: '3px 8px', borderRadius: '20px', fontSize: '11px', fontWeight: 700,
            }}>
              ✗ {errorCount} error{errorCount !== 1 ? 's' : ''}
            </span>
          )}
          {!validationLoading && warningCount > 0 && (
            <span style={{
              background: 'rgba(245,158,11,0.15)', border: '1px solid rgba(245,158,11,0.4)',
              color: '#f59e0b', padding: '3px 8px', borderRadius: '20px', fontSize: '11px', fontWeight: 700,
            }}>
              ⚠ {warningCount} warning{warningCount !== 1 ? 's' : ''}
            </span>
          )}
          {!validationLoading && errorCount === 0 && warningCount === 0 && lastValidatedAt && (
            <span style={{ color: '#34d399', fontSize: '11px' }}>✓ valid</span>
          )}
        </div>
      )}

      {defId && (
        <button onClick={onDelete} disabled={deleting} style={{
          background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.4)',
          color: '#f87171', padding: '6px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
        }}>
          {deleting ? 'Deleting...' : 'Delete Draft'}
        </button>
      )}
      {defId && (
        <button onClick={onValidate} disabled={validating || validationLoading} style={{
          background: 'rgba(52,211,153,0.1)', border: '1px solid rgba(52,211,153,0.4)',
          color: '#34d399', padding: '6px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
        }}>
          {validating || validationLoading ? 'Validating…' : 'Validate'}
        </button>
      )}
      {defId && (
        <button
          onClick={onPublish}
          disabled={publishing || errorCount > 0}
          title={errorCount > 0 ? `Fix ${errorCount} error${errorCount !== 1 ? 's' : ''} before publishing` : undefined}
          style={{
            background: (publishing || errorCount > 0) ? 'rgba(0,240,255,0.05)' : 'rgba(0,240,255,0.15)',
            border: '1px solid rgba(0,240,255,0.4)',
            color: errorCount > 0 ? 'rgba(0,240,255,0.4)' : '#00f0ff',
            padding: '6px 14px', borderRadius: '6px', cursor: errorCount > 0 ? 'not-allowed' : 'pointer', fontSize: '13px',
          }}
        >
          {publishing ? 'Publishing…' : 'Publish'}
        </button>
      )}
      {activeView === 'skill' && (
        <button onClick={onDebugToggle} style={{
          background: debugActive ? 'rgba(245,158,11,0.2)' : 'rgba(100,116,139,0.1)',
          border: `1px solid ${debugActive ? C.amber : C.outline}`,
          color: debugActive ? C.amber : C.textMuted,
          padding: '6px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
        }}>
          {debugActive ? '🐛 Exit Debug' : '🐛 Debug'}
        </button>
      )}
      {activeView === 'agent' && !defId && (
        <>
          <input
            ref={importFileRef}
            type="file"
            accept=".json,application/json"
            style={{ display: 'none' }}
            onChange={onImportFileChange}
          />
          <button onClick={onImportJSON} style={{
            background: 'rgba(99,102,241,0.12)', border: `1px solid rgba(99,102,241,0.5)`,
            color: C.indigo, padding: '7px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
          }}>
            ↓ Import JSON
          </button>
        </>
      )}
      <button onClick={onUndo} disabled={!canUndo} title="Undo" style={{
        background: 'transparent', border: `1px solid ${canUndo ? C.outline : 'transparent'}`,
        color: canUndo ? '#cbd5e1' : '#334155', padding: '6px 10px', borderRadius: '6px',
        cursor: canUndo ? 'pointer' : 'default', fontSize: '14px',
      }}>↩</button>
      <button onClick={onRedo} disabled={!canRedo} title="Redo" style={{
        background: 'transparent', border: `1px solid ${canRedo ? C.outline : 'transparent'}`,
        color: canRedo ? '#cbd5e1' : '#334155', padding: '6px 10px', borderRadius: '6px',
        cursor: canRedo ? 'pointer' : 'default', fontSize: '14px',
      }}>↪</button>
      <button onClick={onExport} title="Export as JSON file" style={{
        background: 'rgba(99,102,241,0.12)', border: `1px solid rgba(99,102,241,0.5)`,
        color: C.indigo, padding: '7px 14px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
      }}>↑ Export JSON</button>
      <button onClick={onSave} disabled={saving} style={{
        background: dirty ? C.cyan : 'rgba(0,240,255,0.2)',
        border: 'none', color: '#000', fontWeight: 700,
        padding: '7px 20px', borderRadius: '6px', cursor: 'pointer', fontSize: '13px',
        opacity: saving ? 0.7 : 1,
      }}>
        {saving ? 'Saving...' : defId ? 'Save Changes' : 'Create Draft'}
      </button>
    </div>
  );
}
