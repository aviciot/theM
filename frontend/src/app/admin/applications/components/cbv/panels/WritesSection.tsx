'use client';
import { useEffect, useState } from 'react';
import type { Node } from '@xyflow/react';
import { C } from '../../../constants';
import { themApi, type MiddlewareWiring } from '@/lib/api';
import { guardWriteVarsForNode } from '../guardWriteVars';

// ── WritesSection — "WRITES" panel for agent/llm nodes ──────────────────────
//
// docs/APPFLOW_GUARD_OUTPUT_PORTS_PLAN.md Phase 2. Mirrors
// InlinePortsSection.tsx's "Reads" section visually, but the data source is
// different: static writes (llm's own output_var) are known synchronously
// from node config, but guard-produced vars depend on live middleware_wirings
// rows (which guards are actually enabled on this node) — same fetch
// AgentGuardsSection.tsx already makes per guard-def form, done once here
// for the whole node instead.
//
// Read-only display only, for now — dragging one of these vars into a
// condition/llm field (the "source-side port-picker popover") is a
// separate, not-yet-built follow-up noted in the plan doc.

interface Props {
  appId: string;
  selectedNode: Node;
  outputVar?: string; // llm's own output_var; omitted for agent (writes no static var)
}

export function WritesSection({ appId, selectedNode, outputVar }: Props) {
  const [wirings, setWirings] = useState<MiddlewareWiring[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    setWirings(null);
    themApi.listMiddlewareWirings(appId).then(list => {
      if (!cancelled) setWirings(list);
    }).catch(() => { if (!cancelled) setWirings([]); });
    return () => { cancelled = true; };
  }, [appId, selectedNode.id]);

  const guardVars = wirings ? guardWriteVarsForNode(selectedNode.id, wirings) : [];
  const hasOutputVar = !!outputVar;
  const isEmpty = !hasOutputVar && guardVars.length === 0;

  return (
    <div>
      <div style={{ fontSize: 11, fontWeight: 700, letterSpacing: '0.08em', color: C.cyan, marginBottom: 6, textTransform: 'uppercase' }}>
        Writes {isEmpty && wirings !== null && (
          <span style={{ color: C.textMuted, fontWeight: 400, textTransform: 'none' }}>— no variables produced</span>
        )}
      </div>

      {hasOutputVar && (
        <div
          style={{
            borderRadius: 6, marginBottom: 4,
            background: 'rgba(0,240,255,0.05)', border: '1px solid rgba(0,240,255,0.15)',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '5px 8px' }}>
            <code style={{ color: C.cyan, fontSize: 11, fontFamily: 'monospace' }}>{`{{.${outputVar}}}`}</code>
          </div>
        </div>
      )}

      {wirings === null && !hasOutputVar && (
        <div style={{ fontSize: 11, color: C.textMuted }}>Loading…</div>
      )}

      {guardVars.length > 0 && (
        <div>
          <div style={{ fontSize: 10, fontWeight: 600, color: C.purple, textTransform: 'uppercase', letterSpacing: '0.04em', margin: '8px 0 4px' }}>
            Guards
          </div>
          {guardVars.map(v => (
            <div
              key={v.flatVar}
              title={`Actual FlowVar: {{.${v.flatVar}}}`}
              style={{
                borderRadius: 6, marginBottom: 4,
                background: 'rgba(168,85,247,0.05)', border: '1px solid rgba(168,85,247,0.15)',
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '5px 8px' }}>
                <code style={{ color: C.purple, fontSize: 11, fontFamily: 'monospace' }}>{`{{${v.displayRef}}}`}</code>
                <span style={{ color: C.textMuted, fontSize: 10 }}>from</span>
                <span style={{ color: '#94a3b8', fontSize: 10 }}>{v.guardLabel}</span>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
