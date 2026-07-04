'use client';
import { useState, useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { useAuthStore } from '@/stores/authStore';

export default function LoginPage() {
  const router = useRouter();
  const { login, fetchUser, isLoading, error, clearError } = useAuthStore();
  const isDev = process.env.NODE_ENV === 'development';
  const [email, setEmail] = useState(isDev ? 'admin' : '');
  const [password, setPassword] = useState(isDev ? 'admin123' : '');
  const [showPw, setShowPw] = useState(false);

  useEffect(() => {
    fetchUser().then((ok) => { if (ok) router.replace('/dashboard'); });
  }, []);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    clearError();
    try {
      await login(email, password);
      router.replace('/dashboard');
    } catch {}
  }

  return (
    <>
      <style>{`
        .bg-mesh {
          background:
            radial-gradient(circle at 20% 30%, rgba(22,43,232,.15) 0%, transparent 50%),
            radial-gradient(circle at 80% 70%, rgba(59,77,255,.15) 0%, transparent 50%),
            #05070a;
          background-attachment: fixed;
        }
        @keyframes pulse-glow { 0%,100%{opacity:.4} 50%{opacity:.7} }
        @keyframes grid-move { 0%{background-position:0 0} 100%{background-position:24px 24px} }
        @keyframes slow-spin { from{transform:rotate(0deg)} to{transform:rotate(360deg)} }
        @keyframes core-pulse {
          0%,100%{transform:scale(1);filter:drop-shadow(0 0 8px rgba(59,77,255,.6))}
          50%{transform:scale(1.15);filter:drop-shadow(0 0 15px rgba(59,77,255,.9))}
        }
        @keyframes fade-slide-up { 0%{opacity:0;transform:translateY(20px)} 100%{opacity:1;transform:translateY(0)} }
        .glow-overlay { animation: pulse-glow 8s ease-in-out infinite; }
        .grid-scanning {
          background-image: radial-gradient(rgba(59,77,255,.2) 0.5px, transparent 0.5px);
          background-size: 24px 24px;
          animation: grid-move 4s linear infinite;
        }
        .animate-slow-spin { animation: slow-spin 20s linear infinite; }
        .logo-core { transform-origin: center; animation: core-pulse 4s ease-in-out infinite; }
        .animate-entrance { animation: fade-slide-up 0.8s cubic-bezier(0.16,1,0.3,1) forwards; }
        .brand-input {
          display: block; width: 100%; background: rgba(2,6,23,.5);
          border: 1px solid #334155; border-radius: 8px;
          padding: 10px 12px 10px 40px;
          color: #f1f5f9 !important; font-size: 14px; transition: all .2s;
          -webkit-text-fill-color: #f1f5f9;
        }
        .brand-input:focus { border-color: #3b4dff; box-shadow: 0 0 0 2px rgba(59,77,255,.2); outline: none; }
        .brand-input::placeholder { color: #64748b !important; -webkit-text-fill-color: #64748b; }
      `}</style>

      <main className="min-h-screen flex flex-col items-center justify-center p-6 relative bg-mesh text-slate-200 antialiased overflow-x-hidden">
        <div className="absolute inset-0 z-0 opacity-20 pointer-events-none grid-scanning" />
        <div className="absolute inset-0 z-0 glow-overlay pointer-events-none"
          style={{ background: 'linear-gradient(135deg, rgba(37,99,235,.1) 0%, transparent 50%, rgba(67,56,202,.1) 100%)' }} />

        {/* Brand header */}
        <div className="z-10 text-center mb-10 w-full max-w-sm animate-entrance" style={{ animationDelay: '0.1s', opacity: 0 }}>
          {/* the-M Logo — shield with agent-network M */}
          <div className="mb-6 flex justify-center items-center gap-4">
            <div className="relative flex items-center justify-center">
              {/*
                Shield: heraldic shape — rounded top shoulders, straight sides, pointed bottom.
                M network: 4 outer nodes (top-L, top-R, bottom-L, bottom-R) + center-valley node.
                Lines: top-L→valley, top-R→valley, top-L→bottom-L, top-R→bottom-R (M shape).
                Red dot: center valley (the "meeting point" of the M).
              */}
              <svg width="90" height="100" viewBox="0 0 90 100" xmlns="http://www.w3.org/2000/svg">
                {/* Heraldic shield: flat top with rounded corners, tapers to a point */}
                <path
                  d="M12 10 Q12 4 18 4 L72 4 Q78 4 78 10 L78 52 C78 74 45 96 45 96 C45 96 12 74 12 52 Z"
                  fill="rgba(15,23,42,0.85)"
                  stroke="#e8eaed"
                  strokeWidth="3.5"
                  strokeLinejoin="round"
                />
                {/* M network lines — drawn first so nodes sit on top */}
                {/* top-L(24,28) → valley(45,56) → top-R(66,28) */}
                <line x1="24" y1="28" x2="45" y2="56" stroke="#c8ccd4" strokeWidth="2" strokeLinecap="round"/>
                <line x1="45" y1="56" x2="66" y2="28" stroke="#c8ccd4" strokeWidth="2" strokeLinecap="round"/>
                {/* top-L → bottom-L(24,72), top-R → bottom-R(66,72) */}
                <line x1="24" y1="28" x2="24" y2="72" stroke="#c8ccd4" strokeWidth="2" strokeLinecap="round"/>
                <line x1="66" y1="28" x2="66" y2="72" stroke="#c8ccd4" strokeWidth="2" strokeLinecap="round"/>
                {/* M network nodes */}
                <circle cx="24" cy="28" r="4.5" fill="#e8eaed" />
                <circle cx="66" cy="28" r="4.5" fill="#e8eaed" />
                <circle cx="24" cy="72" r="4.5" fill="#e8eaed" />
                <circle cx="66" cy="72" r="4.5" fill="#e8eaed" />
                {/* Center valley — red dot */}
                <circle cx="45" cy="56" r="6" fill="#e63946" className="logo-core" />
              </svg>
            </div>
            <h1 style={{
              fontSize: '42px', fontWeight: 900, letterSpacing: '-0.04em',
              color: '#e8eaed', lineHeight: 1, userSelect: 'none',
            }}>
              the-<span style={{ color: '#3b4dff' }}>M</span>
            </h1>
          </div>

          <p className="font-bold tracking-widest uppercase text-[10px] mb-4" style={{ color: '#3b4dff' }}>
            Multi-Agent Orchestration Platform
          </p>
          <div className="space-y-1">
            <h2 className="text-xl font-semibold text-white">Welcome back</h2>
            <p className="text-slate-400 text-sm">Sign in to access the command center</p>
          </div>
        </div>

        {/* Login card */}
        <section className="z-10 w-full max-w-sm animate-entrance" style={{ animationDelay: '0.2s', opacity: 0 }}>
          <div style={{
            background: 'rgba(15,17,23,.7)',
            backdropFilter: 'blur(20px)',
            border: '1px solid rgba(255,255,255,.08)',
            borderRadius: '16px',
            padding: '32px',
            boxShadow: '0 25px 50px rgba(0,0,0,.5)',
          }}>
            {error && (
              <div className="mb-5 flex items-start gap-2 rounded-lg p-3"
                style={{ background: 'rgba(220,38,38,.1)', border: '1px solid rgba(220,38,38,.3)' }}>
                <svg className="w-4 h-4 mt-0.5 shrink-0 text-red-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z" />
                </svg>
                <p className="text-sm text-red-400">{error}</p>
              </div>
            )}

            <form onSubmit={handleSubmit} className="space-y-5">
              <div className="space-y-1.5">
                <label className="block text-sm font-medium text-slate-300" htmlFor="email">Username or email</label>
                <div className="relative">
                  <div className="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none">
                    <svg className="h-5 w-5 text-slate-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M16 12a4 4 0 10-8 0 4 4 0 008 0zm0 0v1.5a2.5 2.5 0 005 0V12a9 9 0 10-9 9m4.5-1.206a8.959 8.959 0 01-4.5 1.207" />
                    </svg>
                  </div>
                  <input
                    id="email" type="text" required autoComplete="username"
                    placeholder="admin or admin@them.local"
                    value={email} onChange={(e) => setEmail(e.target.value)}
                    className="brand-input"
                  />
                </div>
              </div>

              <div className="space-y-1.5">
                <label className="block text-sm font-medium text-slate-300" htmlFor="password">Password</label>
                <div className="relative">
                  <div className="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none">
                    <svg className="h-5 w-5 text-slate-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
                    </svg>
                  </div>
                  <input
                    id="password" type={showPw ? 'text' : 'password'} required autoComplete="current-password"
                    placeholder="••••••••"
                    value={password} onChange={(e) => setPassword(e.target.value)}
                    className="brand-input" style={{ paddingRight: '40px' }}
                  />
                  <button type="button" onClick={() => setShowPw(!showPw)}
                    className="absolute inset-y-0 right-0 pr-3 flex items-center text-slate-500 hover:text-slate-300">
                    {showPw
                      ? <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13.875 18.825A10.05 10.05 0 0112 19c-4.478 0-8.268-2.943-9.543-7a9.97 9.97 0 011.563-3.029m5.858.908a3 3 0 114.243 4.243M9.878 9.878l4.242 4.242M9.88 9.88l-3.29-3.29m7.532 7.532l3.29 3.29M3 3l3.59 3.59m0 0A9.953 9.953 0 0112 5c4.478 0 8.268 2.943 9.543 7a10.025 10.025 0 01-4.132 5.411m0 0L21 21" /></svg>
                      : <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" /><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z" /></svg>
                    }
                  </button>
                </div>
              </div>

              <button type="submit" disabled={isLoading}
                className="w-full flex items-center justify-center gap-2 py-3 px-4 rounded-lg text-sm font-bold text-white transition-all active:scale-[0.98] disabled:opacity-70"
                style={{
                  background: isLoading ? '#1d4ed8' : 'linear-gradient(135deg, #2563eb 0%, #4338ca 100%)',
                  boxShadow: '0 4px 15px rgba(59,77,255,.35)',
                }}>
                {isLoading
                  ? <><svg className="animate-spin h-4 w-4" fill="none" viewBox="0 0 24 24"><circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4"/><path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"/></svg>Signing in…</>
                  : <>Sign in <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M14 5l7 7m0 0l-7 7m7-7H3"/></svg></>
                }
              </button>
            </form>
          </div>

          <p className="mt-6 text-center text-xs text-slate-600">
            the-M · Multi-Agent Runtime
          </p>
        </section>
      </main>
    </>
  );
}
