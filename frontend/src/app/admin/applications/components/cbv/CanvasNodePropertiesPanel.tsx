'use client';
import type { Node, Edge } from '@xyflow/react';
import { C } from '../../constants';
import type { MCPServer, Agent, MiddlewareWiring } from '@/lib/api';
import { OrchestratorNodePanel } from './panels/OrchestratorNodePanel';
import { AgentNodePanel } from './panels/AgentNodePanel';
import { EntryPointNodePanel } from './panels/EntryPointNodePanel';
import { FlowControlNodePanel } from './panels/FlowControlNodePanel';
import { InlineNodePanel } from './panels/InlineNodePanel';
import { CycleNodePanel } from './panels/CycleNodePanel';
import { CanvasTransformPanel } from './panels/CanvasTransformPanel';

// ── CanvasNodePropertiesPanel ────────────────────────────────────────────────

interface Props {
  appId: string;
  selectedNode: Node | null;
  nodes: Node[];
  edges: Edge[];
  agents: Agent[];
  wirings: MiddlewareWiring[];
  openSections: Record<string, boolean>;
  setOpenSections: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  availableMCPServers: MCPServer[];
  mcpExpanded: Record<string, boolean>;
  setMcpExpanded: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  configPanelText: string;
  setConfigPanelText: React.Dispatch<React.SetStateAction<string>>;
  configPanelErr: boolean;
  setConfigPanelErr: React.Dispatch<React.SetStateAction<boolean>>;
  setNodes: (updater: (ns: Node[]) => Node[]) => void;
  setIsDirty: (v: boolean) => void;
  setLogoResult: (v: 'none' | 'valid' | 'invalid' | 'warn') => void;
  showToast: (msg: string, ok: boolean) => void;
  setEpConfig: (instanceId: string, patch: Record<string, unknown>, remove?: string[]) => void;
  onWiringChanged?: () => void;
}

export function CanvasNodePropertiesPanel({
  appId, selectedNode, nodes, edges, agents, wirings,
  openSections, setOpenSections,
  availableMCPServers, mcpExpanded, setMcpExpanded,
  configPanelText, setConfigPanelText, configPanelErr, setConfigPanelErr,
  setNodes, setIsDirty, setLogoResult,
  showToast, setEpConfig, onWiringChanged,
}: Props) {

  if (!selectedNode) {
    return (
      <div style={{ padding: 20, color: C.textMuted, fontSize: 13, fontStyle: 'italic' }}>
        Select a node to configure properties
      </div>
    );
  }

  if (selectedNode.type === 'cycle') {
    return (
      <CycleNodePanel
        selectedNode={selectedNode}
        nodes={nodes}
        setNodes={setNodes}
        setIsDirty={setIsDirty}
      />
    );
  }

  if (selectedNode.type === 'orchestrator') {
    return (
      <OrchestratorNodePanel
        selectedNode={selectedNode}
        nodes={nodes}
        edges={edges}
        openSections={openSections}
        setOpenSections={setOpenSections}
        availableMCPServers={availableMCPServers}
        mcpExpanded={mcpExpanded}
        setMcpExpanded={setMcpExpanded}
        setNodes={setNodes}
        setIsDirty={setIsDirty}
        setLogoResult={setLogoResult}
      />
    );
  }

  if (selectedNode.type === 'agent') {
    return (
      <AgentNodePanel
        appId={appId}
        selectedNode={selectedNode}
        nodes={nodes}
        agents={agents}
        wirings={wirings}
        onWiringChanged={onWiringChanged}
        configPanelText={configPanelText}
        setConfigPanelText={setConfigPanelText}
        configPanelErr={configPanelErr}
        setConfigPanelErr={setConfigPanelErr}
        setNodes={setNodes}
        setIsDirty={setIsDirty}
        showToast={showToast}
      />
    );
  }

  if (selectedNode.type === 'entryPoint') {
    return (
      <EntryPointNodePanel
        selectedNode={selectedNode}
        nodes={nodes}
        edges={edges}
        openSections={openSections}
        setOpenSections={setOpenSections}
        setNodes={setNodes}
        setIsDirty={setIsDirty}
        setLogoResult={setLogoResult}
        setEpConfig={setEpConfig}
      />
    );
  }

  if (selectedNode.type === 'flowControl') {
    return (
      <FlowControlNodePanel
        selectedNode={selectedNode}
        nodes={nodes}
        setNodes={setNodes}
        setIsDirty={setIsDirty}
        setLogoResult={setLogoResult}
      />
    );
  }

  if (selectedNode.type === 'inline') {
    const nodeType = (selectedNode.data as Record<string, unknown>).node_type as string | undefined;
    if (nodeType === 'transform') {
      return (
        <CanvasTransformPanel
          selectedNode={selectedNode}
          nodes={nodes}
          edges={edges}
          setNodes={setNodes}
          setIsDirty={setIsDirty}
        />
      );
    }
    return (
      <InlineNodePanel
        appId={appId}
        selectedNode={selectedNode}
        nodes={nodes}
        edges={edges}
        agents={agents}
        wirings={wirings}
        onWiringChanged={onWiringChanged}
        setNodes={setNodes}
        setIsDirty={setIsDirty}
        setLogoResult={setLogoResult}
        showToast={showToast}
      />
    );
  }

  return (
    <div style={{ padding: 20, color: C.textMuted, fontSize: 13, fontStyle: 'italic' }}>
      Select a node to configure properties
    </div>
  );
}
