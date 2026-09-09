'use client';
import { use, useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { AppDetailShell } from '../AppDetailShell';
import { AppPageLoader, AppPageError } from '../AppPageLoader';
import { useAppById } from '../useAppById';
import { MonitorView } from '../../components/MonitorView';

export default function AppMonitorPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const router = useRouter();
  const { app, agents, loading, error } = useAppById(id);
  const [token, setToken] = useState<string | null>(null);

  useEffect(() => {
    fetch('/api/auth/token')
      .then(r => r.ok ? r.json() : null)
      .then((d: { token?: string } | null) => { if (d?.token) setToken(d.token); })
      .catch(() => {});
  }, []);

  if (loading) return <AppPageLoader />;
  if (error || !app) return <AppPageError message={error ?? 'Application not found'} />;

  return (
    <AppDetailShell app={app}>
      <MonitorView
        app={app}
        agents={agents}
        token={token}
        onBack={() => router.push('/admin/applications')}
      />
    </AppDetailShell>
  );
}
