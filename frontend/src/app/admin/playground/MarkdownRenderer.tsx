'use client';
import { useEffect, useRef, useState } from 'react';
import type { Segment, Block } from './playgroundTypes';

declare global {
  interface Window {
    mermaid?: {
      initialize: (cfg: object) => void;
      render: (id: string, code: string) => Promise<{ svg: string }>;
      _initialized?: boolean;
    };
  }
}

function MermaidBlock({ code }: { code: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const [svg, setSvg] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function render() {
      if (!window.mermaid) {
        await new Promise<void>((resolve, reject) => {
          const s = document.createElement('script');
          s.src = 'https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js';
          s.onload = () => resolve();
          s.onerror = reject;
          document.head.appendChild(s);
        });
      }
      if (!window.mermaid!._initialized) {
        window.mermaid!.initialize({ startOnLoad: false, theme: 'dark' });
        window.mermaid!._initialized = true;
      }
      try {
        const id = `mmd-${Math.random().toString(36).slice(2)}`;
        const { svg } = await window.mermaid!.render(id, code);
        if (!cancelled) setSvg(svg);
      } catch (e) {
        if (!cancelled) setErr(String(e));
      }
    }

    render();
    return () => { cancelled = true; };
  }, [code]);

  if (err) return (
    <pre style={{ color: '#f87171', fontSize: 12, whiteSpace: 'pre-wrap', margin: '8px 0' }}>{err}</pre>
  );
  if (!svg) return (
    <div style={{ color: 'var(--tm-text-muted)', fontSize: 12, padding: '8px 0' }}>Rendering diagram…</div>
  );
  return (
    <div
      dangerouslySetInnerHTML={{ __html: svg }}
      style={{ overflowX: 'auto', margin: '8px 0', lineHeight: 1 }}
    />
  );
}

function CodeBlock({ lang, code }: { lang: string; code: string }) {
  const [copied, setCopied] = useState(false);

  if (lang === 'mermaid') return <MermaidBlock code={code} />;

  return (
    <div style={{
      position: 'relative',
      background: 'rgba(0,0,0,0.35)',
      border: '1px solid var(--tm-border)',
      borderRadius: 8,
      margin: '8px 0',
      overflow: 'hidden',
    }}>
      {lang && (
        <div style={{
          padding: '4px 12px',
          fontSize: 11,
          fontFamily: 'monospace',
          color: 'var(--tm-text-muted)',
          borderBottom: '1px solid var(--tm-border)',
          background: 'rgba(0,0,0,0.2)',
        }}>{lang}</div>
      )}
      <button
        onClick={() => { navigator.clipboard.writeText(code); setCopied(true); setTimeout(() => setCopied(false), 2000); }}
        style={{
          position: 'absolute', top: lang ? 28 : 6, right: 8,
          padding: '2px 8px', fontSize: 11, borderRadius: 4, border: '1px solid var(--tm-border)',
          background: 'rgba(0,0,0,0.4)', color: 'var(--tm-text-muted)', cursor: 'pointer',
        }}
      >{copied ? 'Copied!' : 'Copy'}</button>
      <pre style={{
        margin: 0, padding: '10px 12px',
        fontSize: 12, fontFamily: 'monospace',
        color: 'var(--tm-text)', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
        overflowX: 'auto',
      }}>{code}</pre>
    </div>
  );
}

function parseInline(text: string): Segment[] {
  const out: Segment[] = [];
  const re = /(\*\*(.+?)\*\*|__(.+?)__|(?<!\*)\*(?!\*)(.+?)(?<!\*)\*(?!\*)|_(.+?)_|`([^`]+)`)/g;
  let last = 0, m: RegExpExecArray | null;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) out.push({ t: 'text', v: text.slice(last, m.index) });
    if (m[2] || m[3]) out.push({ t: 'bold',   v: m[2] || m[3] });
    else if (m[4] || m[5]) out.push({ t: 'italic', v: m[4] || m[5] });
    else if (m[6]) out.push({ t: 'code', v: m[6] });
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push({ t: 'text', v: text.slice(last) });
  return out;
}

function InlineText({ text }: { text: string }) {
  const segs = parseInline(text);
  return (
    <>
      {segs.map((s, i) => {
        if (s.t === 'bold')   return <strong key={i}>{s.v}</strong>;
        if (s.t === 'italic') return <em key={i}>{s.v}</em>;
        if (s.t === 'code')   return (
          <code key={i} style={{
            fontFamily: 'monospace', fontSize: '0.85em',
            background: 'rgba(255,255,255,0.08)', borderRadius: 4, padding: '1px 5px',
          }}>{s.v}</code>
        );
        return <span key={i}>{s.v}</span>;
      })}
    </>
  );
}

function parseBlocks(raw: string): Block[] {
  const lines = raw.split('\n');
  const blocks: Block[] = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    if (line.startsWith('```')) {
      const lang = line.slice(3).trim();
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !lines[i].startsWith('```')) {
        codeLines.push(lines[i]);
        i++;
      }
      i++;
      blocks.push({ t: 'code', lang, code: codeLines.join('\n') });
      continue;
    }

    if (/^(\*{3,}|-{3,}|_{3,})\s*$/.test(line)) {
      blocks.push({ t: 'hr' });
      i++; continue;
    }

    const h4 = line.match(/^####\s+(.*)/); if (h4) { blocks.push({ t: 'h4', text: h4[1] }); i++; continue; }
    const h3 = line.match(/^###\s+(.*)/);  if (h3) { blocks.push({ t: 'h3', text: h3[1] }); i++; continue; }
    const h2 = line.match(/^##\s+(.*)/);   if (h2) { blocks.push({ t: 'h2', text: h2[1] }); i++; continue; }
    const h1 = line.match(/^#\s+(.*)/);    if (h1) { blocks.push({ t: 'h1', text: h1[1] }); i++; continue; }

    if (/^[-*+]\s/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^[-*+]\s/.test(lines[i])) {
        items.push(lines[i].replace(/^[-*+]\s+/, ''));
        i++;
      }
      blocks.push({ t: 'ul', items });
      continue;
    }

    if (/^\d+\.\s/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\d+\.\s/.test(lines[i])) {
        items.push(lines[i].replace(/^\d+\.\s+/, ''));
        i++;
      }
      blocks.push({ t: 'ol', items });
      continue;
    }

    if (line.trim() === '') { i++; continue; }

    const paraLines: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== '' &&
      !/^(#{1,4}\s|[-*+]\s|\d+\.\s|```|(\*{3,}|-{3,}|_{3,})\s*$)/.test(lines[i])
    ) {
      paraLines.push(lines[i]);
      i++;
    }
    if (paraLines.length) blocks.push({ t: 'p', text: paraLines.join(' ') });
  }

  return blocks;
}

export function MarkdownText({ text }: { text: string }) {
  const blocks = parseBlocks(text);

  return (
    <div style={{ fontSize: 14, lineHeight: 1.65, color: 'var(--tm-text)' }}>
      {blocks.map((b, i) => {
        switch (b.t) {
          case 'h1': return <h1 key={i} style={{ fontSize: 20, fontWeight: 700, margin: '16px 0 6px', color: 'var(--tm-text)' }}><InlineText text={b.text} /></h1>;
          case 'h2': return <h2 key={i} style={{ fontSize: 17, fontWeight: 700, margin: '14px 0 5px', color: 'var(--tm-text)', borderBottom: '1px solid var(--tm-border)', paddingBottom: 4 }}><InlineText text={b.text} /></h2>;
          case 'h3': return <h3 key={i} style={{ fontSize: 15, fontWeight: 600, margin: '12px 0 4px', color: 'var(--tm-text)' }}><InlineText text={b.text} /></h3>;
          case 'h4': return <h4 key={i} style={{ fontSize: 14, fontWeight: 600, margin: '10px 0 3px', color: 'var(--tm-text-muted)' }}><InlineText text={b.text} /></h4>;
          case 'hr': return <hr key={i} style={{ border: 'none', borderTop: '1px solid var(--tm-border)', margin: '14px 0' }} />;
          case 'code': return <CodeBlock key={i} lang={b.lang} code={b.code} />;
          case 'ul': return (
            <ul key={i} style={{ margin: '6px 0', paddingLeft: 20 }}>
              {b.items.map((item, j) => (
                <li key={j} style={{ margin: '3px 0' }}><InlineText text={item} /></li>
              ))}
            </ul>
          );
          case 'ol': return (
            <ol key={i} style={{ margin: '6px 0', paddingLeft: 20 }}>
              {b.items.map((item, j) => (
                <li key={j} style={{ margin: '3px 0' }}><InlineText text={item} /></li>
              ))}
            </ol>
          );
          case 'p': return <p key={i} style={{ margin: '6px 0' }}><InlineText text={b.text} /></p>;
        }
      })}
    </div>
  );
}
