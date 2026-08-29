'use client';
import AuthGuard from '@/components/AuthGuard';
import Sidebar from '@/components/Sidebar';
import { ReactFlowProvider } from '@xyflow/react';
import { BuilderWorkspace } from './components/BuilderWorkspace';

export default function AgentBuilderPage() {
  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--tm-bg)' }}>
        <Sidebar />
        <div style={{ marginLeft: 260, flex: 1, display: 'flex', flexDirection: 'column', height: '100vh', overflow: 'hidden' }}>
          <ReactFlowProvider>
            <BuilderWorkspace />
          </ReactFlowProvider>
        </div>
      </div>
    </AuthGuard>
  );
}
