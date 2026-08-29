'use client';
import { createContext, useContext } from 'react';

// Incrementing token — StepNode closes its PortsPopover whenever this changes.
// BuilderWorkspace increments it on onNodeClick and onPaneClick.
export const PortsPanelContext = createContext<number>(0);

export function usePortsPanelCloseToken(): number {
  return useContext(PortsPanelContext);
}
