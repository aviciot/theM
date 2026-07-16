'use client';
/**
 * ChromaGrid — spotlight-reveal effect wrapper.
 *
 * Renders children normally, then overlays the chroma effect on top:
 *  - A grayscale+darken mask covers the grid, with the hole following the cursor.
 *  - Cards near the cursor reveal their full color gradient; distant cards stay muted.
 *  - Per-card inner highlight tracked via --mouse-x / --mouse-y on .chroma-card elements.
 *
 * Usage:
 *   Wrap your card grid in <ChromaGrid>. Mark each card element with className="chroma-card"
 *   and set CSS vars --card-gradient and --card-border on it for the color reveal.
 *
 * Zero external dependencies — damping via lerp + rAF.
 */

import { useRef, useEffect, useCallback, ReactNode } from 'react';

interface ChromaGridProps {
  children: ReactNode;
  radius?: number;
  damping?: number;
  fadeOutMs?: number;
  className?: string;
  style?: React.CSSProperties;
  onDragOver?: (e: React.DragEvent) => void;
  onDragLeave?: (e: React.DragEvent) => void;
  onDrop?: (e: React.DragEvent) => void;
}

export default function ChromaGrid({
  children,
  radius = 420,
  damping = 0.1,
  fadeOutMs = 700,
  className = '',
  style,
  onDragOver,
  onDragLeave,
  onDrop,
}: ChromaGridProps) {
  const rootRef = useRef<HTMLDivElement>(null);
  const fadeRef = useRef<HTMLDivElement>(null);
  const rafRef = useRef<number>(0);
  const cur = useRef({ x: 0, y: 0 });
  const tgt = useRef({ x: 0, y: 0 });
  const inside = useRef(false);

  const applyPos = useCallback((x: number, y: number) => {
    rootRef.current?.style.setProperty('--x', `${x}px`);
    rootRef.current?.style.setProperty('--y', `${y}px`);
  }, []);

  const tick = useCallback(() => {
    cur.current.x += (tgt.current.x - cur.current.x) * damping;
    cur.current.y += (tgt.current.y - cur.current.y) * damping;
    applyPos(cur.current.x, cur.current.y);
    rafRef.current = requestAnimationFrame(tick);
  }, [damping, applyPos]);

  useEffect(() => {
    const elRaw = rootRef.current;
    if (!elRaw) return;
    const node: HTMLDivElement = elRaw; // non-null alias for closures

    // Seed at center so first entry isn't jarring
    const { width, height } = node.getBoundingClientRect();
    cur.current = { x: width / 2, y: height / 2 };
    tgt.current = { x: width / 2, y: height / 2 };
    applyPos(width / 2, height / 2);

    function onMove(e: PointerEvent) {
      const r = node.getBoundingClientRect();
      tgt.current = { x: e.clientX - r.left, y: e.clientY - r.top };

      // Per-card inner highlight
      const card = (e.target as HTMLElement).closest<HTMLElement>('.chroma-card');
      if (card && node.contains(card)) {
        const cr = card.getBoundingClientRect();
        card.style.setProperty('--mouse-x', `${e.clientX - cr.left}px`);
        card.style.setProperty('--mouse-y', `${e.clientY - cr.top}px`);
      }

      if (!inside.current) {
        inside.current = true;
        if (fadeRef.current) {
          fadeRef.current.style.transition = 'opacity 0.2s ease';
          fadeRef.current.style.opacity = '0';
        }
        rafRef.current = requestAnimationFrame(tick);
      }
    }

    function onLeave() {
      inside.current = false;
      cancelAnimationFrame(rafRef.current);
      if (fadeRef.current) {
        fadeRef.current.style.transition = `opacity ${fadeOutMs}ms ease`;
        fadeRef.current.style.opacity = '1';
      }
    }

    node.addEventListener('pointermove', onMove);
    node.addEventListener('pointerleave', onLeave);
    return () => {
      node.removeEventListener('pointermove', onMove);
      node.removeEventListener('pointerleave', onLeave);
      cancelAnimationFrame(rafRef.current);
    };
  }, [tick, applyPos, fadeOutMs]);

  return (
    <div
      ref={rootRef}
      className={`chroma-grid ${className}`}
      style={{ position: 'relative', '--r': `${radius}px`, ...style } as React.CSSProperties}
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
    >
      {children}
      <div className="chroma-overlay" />
      <div ref={fadeRef} className="chroma-fade" />
    </div>
  );
}
