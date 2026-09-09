'use client';
import { useRouter } from 'next/navigation';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { C } from '../constants';

export function AppPageLoader() {
  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: C.bg }}>
        <Sidebar />
        <div style={{ marginLeft: 260, flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          <span className="material-symbols-outlined" style={{ fontSize: 32, color: C.textMuted, animation: 'spin .8s linear infinite' }}>sync</span>
        </div>
      </div>
    </AuthGuard>
  );
}

export function AppPageError({ message }: { message: string }) {
  const router = useRouter();
  return (
    <AuthGuard>
      <div style={{ display: 'flex', minHeight: '100vh', background: C.bg }}>
        <Sidebar />
        <div style={{ marginLeft: 260, flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 16 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 36, color: '#f87171' }}>error</span>
          <p style={{ color: '#f87171', fontSize: 14, margin: 0 }}>{message}</p>
          <button onClick={() => router.push('/admin/applications')} style={{ padding: '8px 18px', borderRadius: 8, background: 'rgba(248,113,113,0.1)', border: '1px solid rgba(248,113,113,0.3)', color: '#f87171', cursor: 'pointer', fontSize: 13 }}>
            Back to Applications
          </button>
        </div>
      </div>
    </AuthGuard>
  );
}
