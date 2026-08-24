'use client';
import { createContext, useContext } from 'react';
import type { LayoutDir } from './types';

export const LayoutDirContext = createContext<LayoutDir>('TB');

export function useLayoutDir(): LayoutDir {
  return useContext(LayoutDirContext);
}
