'use client';
import { useEffect, useState } from 'react';
import { themApi, type Application, type Agent } from '@/lib/api';

export function useAppById(id: string) {
  const [app, setApp] = useState<Application | null>(null);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    Promise.all([themApi.getApplication(id), themApi.agents()])
      .then(([a, ags]) => {
        if (cancelled) return;
        setApp(a);
        setAgents(ags);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : 'Failed to load');
      })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [id]);

  return { app, agents, loading, error, setApp };
}
