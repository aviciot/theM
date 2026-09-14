'use client';
import { useEffect, useRef, useState } from 'react';
import type { RunEvent, UserResult } from './types';

export function useRunStream(runId: string | null) {
  const [users, setUsers] = useState<UserResult[]>([]);
  const [summary, setSummary] = useState<import('./types').RunSummary | null>(null);
  const [done, setDone] = useState(false);
  const esRef = useRef<EventSource | null>(null);

  useEffect(() => {
    if (!runId) return;
    setUsers([]);
    setSummary(null);
    setDone(false);

    const es = new EventSource(`/api/backend/run/${runId}/stream`);
    esRef.current = es;

    es.onmessage = (e) => {
      const ev: RunEvent = JSON.parse(e.data);
      if (ev.type === 'user_update' && ev.user_result) {
        const ur = ev.user_result;
        setUsers((prev) => {
          const next = [...prev];
          next[ur.user_index] = ur;
          return next;
        });
      } else if (ev.type === 'run_complete' && ev.summary) {
        setSummary(ev.summary);
        if (ev.summary.results) {
          setUsers(ev.summary.results);
        }
        setDone(true);
        es.close();
      }
    };

    es.onerror = () => {
      setDone(true);
      es.close();
    };

    return () => {
      es.close();
    };
  }, [runId]);

  return { users, summary, done };
}
