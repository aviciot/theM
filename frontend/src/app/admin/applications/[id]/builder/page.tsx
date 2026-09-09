'use client';
import { use } from 'react';
import { useRouter } from 'next/navigation';
import { AppDetailShell } from '../AppDetailShell';
import { AppPageLoader, AppPageError } from '../AppPageLoader';
import { useAppById } from '../useAppById';
import { CanvasBuilderView } from '../../components/CanvasBuilderView';

export default function AppBuilderPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const router = useRouter();
  const { app, agents, setApp, loading, error } = useAppById(id);

  if (loading) return <AppPageLoader />;
  if (error || !app) return <AppPageError message={error ?? 'Application not found'} />;

  return (
    <AppDetailShell app={app}>
      <CanvasBuilderView
        app={app}
        agents={agents}
        onBack={() => router.push('/admin/applications')}
        onAppUpdated={(updated) => setApp(updated)}
      />
    </AppDetailShell>
  );
}
