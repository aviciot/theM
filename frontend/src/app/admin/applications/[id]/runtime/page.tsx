'use client';
import { use } from 'react';
import { useRouter } from 'next/navigation';
import { AppDetailShell } from '../AppDetailShell';
import { AppPageLoader, AppPageError } from '../AppPageLoader';
import { useAppById } from '../useAppById';
import { RuntimeView } from '../../components/RuntimeView';

export default function AppRuntimePage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const router = useRouter();
  const { app, loading, error } = useAppById(id);

  if (loading) return <AppPageLoader />;
  if (error || !app) return <AppPageError message={error ?? 'Application not found'} />;

  return (
    <AppDetailShell app={app}>
      <RuntimeView
        app={app}
        onBack={() => router.push('/admin/applications')}
      />
    </AppDetailShell>
  );
}
