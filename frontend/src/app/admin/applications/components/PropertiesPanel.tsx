'use client';
import { useState } from 'react';
import type { Node, Edge } from '@xyflow/react';
import type { Application, ChainStatus } from '../types';
import { C, glass } from '../constants';
import { AppPanel }          from './panel/AppPanel';
import { EntryPointPanel }   from './panel/EntryPointPanel';
import { OrchestratorPanel } from './panel/OrchestratorPanel';
import { AgentPanel }        from './panel/AgentPanel';
import { MiddlewarePanel }   from './panel/MiddlewarePanel';

export function PropertiesPanel({
  selectedNode,
  onUpdateNode,
  slugLocked,
  onSlugManualEdit,
  appName,
  onAppNameChange,
  convTokenLimit,
  onConvTokenLimitChange,
  chain,
  app,
  epCount,
  nodes,
  edges,
}: {
  selectedNode: Node | null;
  onUpdateNode: (id: string, data: Record<string, unknown>) => void;
  slugLocked: boolean;
  onSlugManualEdit: () => void;
  appName: string;
  onAppNameChange: (name: string) => void;
  convTokenLimit: string;
  onConvTokenLimitChange: (val: string) => void;
  chain: ChainStatus;
  app: Application | null;
  epCount: number;
  nodes: Node[];
  edges: Edge[];
}) {
  const [propTab, setPropTab] = useState<'properties' | 'configuration'>('properties');

  function TabBtn({ id, label }: { id: 'properties' | 'configuration'; label: string }) {
    const active = propTab === id;
    return (
      <button onClick={() => setPropTab(id)} style={{
        padding: '6px 14px', borderRadius: 6, border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 600,
        background: active ? 'rgba(0,240,255,0.15)' : 'transparent',
        color: active ? C.cyan : C.textMuted,
        transition: 'all 0.15s',
      }}>{label}</button>
    );
  }

  return (
    <div onKeyDown={e => e.stopPropagation()} style={{
      width: 320, flexShrink: 0, height: '100%', overflowY: 'auto',
      ...glass, borderLeft: `1px solid ${C.glassBorder}`, padding: '16px 14px',
      display: 'flex', flexDirection: 'column',
    }}>
      <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, letterSpacing: 1, textTransform: 'uppercase', paddingBottom: 8, borderBottom: `1px solid ${C.outlineVariant}`, marginBottom: 16 }}>
        {selectedNode ? 'Node Properties' : 'Application'}
      </div>

      {!selectedNode ? (
        <AppPanel
          appName={appName}
          onAppNameChange={onAppNameChange}
          convTokenLimit={convTokenLimit}
          onConvTokenLimitChange={onConvTokenLimitChange}
          chain={chain}
          app={app}
          epCount={epCount}
        />
      ) : (
        <>
          <div style={{ display: 'flex', gap: 4, marginBottom: 18 }}>
            <TabBtn id="properties" label="Properties" />
            <TabBtn id="configuration" label="Configuration" />
          </div>

          {selectedNode.type === 'entryPoint' && propTab === 'properties' && (
            <EntryPointPanel
              selectedNode={selectedNode}
              onUpdateNode={onUpdateNode}
              slugLocked={slugLocked}
              onSlugManualEdit={onSlugManualEdit}
              app={app}
              nodes={nodes}
              edges={edges}
            />
          )}

          {selectedNode.type === 'orchestrator' && propTab === 'properties' && (
            <OrchestratorPanel
              selectedNode={selectedNode}
              onUpdateNode={onUpdateNode}
              app={app}
              nodes={nodes}
              edges={edges}
            />
          )}

          {selectedNode.type === 'agent' && propTab === 'properties' && (
            <AgentPanel selectedNode={selectedNode} />
          )}

          {selectedNode.type === 'middleware' && propTab === 'properties' && (
            <MiddlewarePanel
              selectedNode={selectedNode}
              onUpdateNode={onUpdateNode}
            />
          )}

          {propTab === 'configuration' && (
            <div style={{ color: C.textMuted, fontSize: 13, padding: 10 }}>
              Configuration options for this node type are managed at the resource level.<br /><br />
              <span style={{ fontSize: 11, opacity: 0.7 }}>Use the Properties tab or navigate to the resource admin page.</span>
            </div>
          )}
        </>
      )}
    </div>
  );
}
