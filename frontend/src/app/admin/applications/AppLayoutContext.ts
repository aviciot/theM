'use client';
import { createContext, useContext } from 'react';

export const AppLayoutDirContext = createContext<'TB' | 'LR'>('TB');

export function useAppLayoutDir(): 'TB' | 'LR' {
  return useContext(AppLayoutDirContext);
}
