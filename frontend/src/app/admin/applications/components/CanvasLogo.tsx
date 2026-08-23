'use client';
import type { LogoState } from '../types';
import { LOGO_STATES, LOGO_KEYFRAMES, LOGO_PATHS, LOGO_COLOR, THINK_DELAYS, THINK_DURATIONS } from '../constants';

export function computeLogoState(s: { loaded: boolean; isDirty: boolean; busy: boolean; lastResult: 'none' | 'valid' | 'invalid' | 'warn' }): LogoState {
  if (s.busy) return 'thinking';
  if (s.lastResult === 'invalid') return 'error';
  if (s.lastResult === 'warn') return 'warning';
  if (s.lastResult === 'valid') return 'success';
  if (!s.loaded) return 'idle';
  if (s.isDirty) return 'dirty';
  return 'idle';
}

export function CanvasLogo({ state }: { state: LogoState }) {
  const def = LOGO_STATES[state];
  const key = (state === 'idle' || state === 'dirty') ? 'calm' : state;
  const isExplode = state === 'success';
  const isThinking = state === 'thinking';

  return (
    <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', pointerEvents: 'none', zIndex: 0 }}>
      <style>{LOGO_KEYFRAMES}</style>
      <svg
        key={key}
        xmlns="http://www.w3.org/2000/svg"
        width={720} height={572}
        viewBox="0 0 1407 1118"
        overflow="visible"
        style={{ opacity: def.opacity, animation: def.animation, filter: def.filter, overflow: 'visible' }}
      >
        {LOGO_PATHS.map(({ id, points, ex, ey }, i) => (
          <polygon
            key={id}
            points={points}
            style={isExplode ? {
              // @ts-ignore
              '--ex': ex,
              '--ey': ey,
              '--rot': `${(ex + ey) * 45}deg`,
              fill: LOGO_COLOR,
              animation: 'logo-explode 1.8s cubic-bezier(0.25,0.46,0.45,0.94) forwards',
              animationDelay: `${i * 0.06}s`,
              transformOrigin: 'center',
              transformBox: 'fill-box',
            } as React.CSSProperties : isThinking ? {
              animation: `logo-polygon-flicker ${THINK_DURATIONS[i]}s ease-in-out ${THINK_DELAYS[i]}s infinite`,
            } as React.CSSProperties : { fill: state === 'warning' ? '#ff8080' : LOGO_COLOR }}
          />
        ))}
      </svg>
    </div>
  );
}
