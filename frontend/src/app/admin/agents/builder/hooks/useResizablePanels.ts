import React, { useState, useRef, useEffect } from 'react';

export function useResizablePanels() {
  const [libraryWidth, setLibraryWidth] = useState(220);
  const [propertiesWidth, setPropertiesWidth] = useState(300);
  const resizingRef = useRef<{ side: 'library' | 'properties'; startX: number; startW: number } | null>(null);

  useEffect(() => {
    function onMouseMove(e: globalThis.MouseEvent) {
      if (!resizingRef.current) return;
      const { side, startX, startW } = resizingRef.current;
      const delta = e.clientX - startX;
      if (side === 'library') {
        setLibraryWidth(Math.max(160, Math.min(480, startW + delta)));
      } else {
        setPropertiesWidth(Math.max(220, Math.min(600, startW - delta)));
      }
    }
    function onMouseUp() { resizingRef.current = null; document.body.style.cursor = ''; document.body.style.userSelect = ''; }
    document.addEventListener('mousemove', onMouseMove);
    document.addEventListener('mouseup', onMouseUp);
    return () => { document.removeEventListener('mousemove', onMouseMove); document.removeEventListener('mouseup', onMouseUp); };
  }, []);

  function startResizeLibrary(e: React.MouseEvent) {
    e.preventDefault();
    resizingRef.current = { side: 'library', startX: e.clientX, startW: libraryWidth };
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  }

  function startResizeProperties(e: React.MouseEvent) {
    e.preventDefault();
    resizingRef.current = { side: 'properties', startX: e.clientX, startW: propertiesWidth };
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  }

  return { libraryWidth, propertiesWidth, startResizeLibrary, startResizeProperties };
}
