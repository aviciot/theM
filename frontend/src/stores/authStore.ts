'use client';
import { create } from 'zustand';
import type { AuthState, OdinUser, TokenResponse } from '@/types/auth';

const AUTH_URL = process.env.NEXT_PUBLIC_AUTH_URL || '/api/auth';

// JWT uses base64url (- and _ instead of + and /) — atob needs standard base64
function decodeJwtPayload(token: string): Record<string, any> {
  const base64url = token.split('.')[1];
  const base64 = base64url.replace(/-/g, '+').replace(/_/g, '/');
  const padded = base64 + '='.repeat((4 - base64.length % 4) % 4);
  return JSON.parse(atob(padded));
}

function decodeUser(token: string): OdinUser {
  const payload = decodeJwtPayload(token);
  return {
    id: parseInt(payload.sub),
    email: payload.username || payload.email,
    name: payload.name || payload.username,
    role: payload.role || 'user',
  };
}

export const useAuthStore = create<AuthState>((set) => ({
  user: null,
  isAuthenticated: false,
  isLoading: false,
  error: null,

  login: async (email, password) => {
    set({ isLoading: true, error: null });
    try {
      const res = await fetch(`${AUTH_URL}/login`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: email, password }),
      });
      if (!res.ok) {
        const err = await res.json().catch(() => ({}));
        throw new Error(err.detail || 'Invalid credentials');
      }
      const data: TokenResponse = await res.json();
      localStorage.setItem('odin_access_token', data.access_token);
      if (data.refresh_token) localStorage.setItem('odin_refresh_token', data.refresh_token);
      set({ user: decodeUser(data.access_token), isAuthenticated: true, isLoading: false, error: null });
    } catch (e: any) {
      set({ user: null, isAuthenticated: false, isLoading: false, error: e.message });
      throw e;
    }
  },

  logout: () => {
    if (typeof window !== 'undefined') {
      localStorage.removeItem('odin_access_token');
      localStorage.removeItem('odin_refresh_token');
    }
    set({ user: null, isAuthenticated: false, error: null });
  },

  fetchUser: async () => {
    if (typeof window === 'undefined') return false;
    const token = localStorage.getItem('odin_access_token');
    if (!token) {
      set({ isAuthenticated: false, user: null, isLoading: false });
      return false;
    }
    try {
      const payload = decodeJwtPayload(token);
      if (payload.exp && payload.exp * 1000 < Date.now()) {
        // Token expired — try refresh
        const refresh = localStorage.getItem('odin_refresh_token');
        if (!refresh) throw new Error('no refresh token');
        const res = await fetch(`${AUTH_URL}/refresh`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ refresh_token: refresh }),
        });
        if (!res.ok) throw new Error('refresh failed');
        const data: TokenResponse = await res.json();
        localStorage.setItem('odin_access_token', data.access_token);
        if (data.refresh_token) localStorage.setItem('odin_refresh_token', data.refresh_token);
        set({ user: decodeUser(data.access_token), isAuthenticated: true, isLoading: false });
        return true;
      }
      // Token still valid — decode and set
      set({ user: decodeUser(token), isAuthenticated: true, isLoading: false });
      return true;
    } catch (err) {
      console.error('[authStore] fetchUser failed:', err);
      localStorage.removeItem('odin_access_token');
      localStorage.removeItem('odin_refresh_token');
      set({ user: null, isAuthenticated: false, isLoading: false });
      return false;
    }
  },

  clearError: () => set({ error: null }),
}));
