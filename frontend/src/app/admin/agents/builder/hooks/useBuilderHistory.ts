import { useCallback, useRef, useState } from 'react';
import type { AgentDefinitionDoc } from '@/lib/api';

interface UseBuilderHistoryParams {
  buildDefinitionDoc: () => AgentDefinitionDoc;
  loadDefinitionDoc: (doc: AgentDefinitionDoc) => void;
  setDirty: (v: boolean) => void;
}

export function useBuilderHistory({ buildDefinitionDoc, loadDefinitionDoc, setDirty }: UseBuilderHistoryParams) {
  const undoStack = useRef<string[]>([]);
  const redoStack = useRef<string[]>([]);
  const [canUndo, setCanUndo] = useState(false);
  const [canRedo, setCanRedo] = useState(false);

  const pushHistory = useCallback((snapshot: string) => {
    undoStack.current.push(snapshot);
    if (undoStack.current.length > 100) undoStack.current.shift();
    redoStack.current = [];
    setCanUndo(true);
    setCanRedo(false);
  }, []);

  const markDirty = useCallback(() => {
    try { pushHistory(JSON.stringify(buildDefinitionDoc())); } catch { /* ignore */ }
    setDirty(true);
  }, [pushHistory, buildDefinitionDoc, setDirty]);

  function handleUndo() {
    const snap = undoStack.current.pop();
    if (!snap) return;
    try { redoStack.current.push(JSON.stringify(buildDefinitionDoc())); } catch { /* ignore */ }
    setCanRedo(true);
    setCanUndo(undoStack.current.length > 0);
    try { loadDefinitionDoc(JSON.parse(snap) as AgentDefinitionDoc); setDirty(true); } catch { /* ignore */ }
  }

  function handleRedo() {
    const snap = redoStack.current.pop();
    if (!snap) return;
    try { undoStack.current.push(JSON.stringify(buildDefinitionDoc())); } catch { /* ignore */ }
    setCanUndo(true);
    setCanRedo(redoStack.current.length > 0);
    try { loadDefinitionDoc(JSON.parse(snap) as AgentDefinitionDoc); setDirty(true); } catch { /* ignore */ }
  }

  return { pushHistory, markDirty, handleUndo, handleRedo, canUndo, canRedo };
}
