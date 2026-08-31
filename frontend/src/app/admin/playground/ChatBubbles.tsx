'use client';
import { useState } from 'react';
import { type ChatMsg } from './playgroundTypes';
import { MarkdownText } from './MarkdownRenderer';

// ── MicIcon ───────────────────────────────────────────────────────────────

export function MicIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
      <path d="M12 14a3 3 0 0 0 3-3V5a3 3 0 0 0-6 0v6a3 3 0 0 0 3 3zm5-3a5 5 0 0 1-10 0H5a7 7 0 0 0 6 6.93V21h2v-3.07A7 7 0 0 0 19 11h-2z"/>
    </svg>
  );
}

// ── Spinner ───────────────────────────────────────────────────────────────

export function Spinner() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round">
      <path d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83" style={{ animation: 'spin 1s linear infinite', transformOrigin: 'center' }} />
    </svg>
  );
}

// ── copyToClipboard ───────────────────────────────────────────────────────

export function copyToClipboard(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) return navigator.clipboard.writeText(text);
  return new Promise((resolve, reject) => {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.cssText = 'position:fixed;top:-9999px;left:-9999px;opacity:0';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(ta);
    ok ? resolve() : reject(new Error('execCommand failed'));
  });
}

// ── MsgBubble ─────────────────────────────────────────────────────────────

export function MsgBubble({ msg, color }: { msg: ChatMsg; color: string }) {
  const [copied, setCopied] = useState(false);
  const [hovered, setHovered] = useState(false);

  function handleCopy() {
    copyToClipboard(msg.text).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }).catch(() => {});
  }

  const isUser = msg.role === 'user';
  const showActions = hovered && msg.text && !msg.pending;

  return (
    <div
      style={{ maxWidth: '78%', display: 'flex', flexDirection: 'column', alignItems: isUser ? 'flex-end' : 'flex-start' }}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      <div style={{ padding: '9px 13px', borderRadius: isUser ? '14px 14px 4px 14px' : '14px 14px 14px 4px', background: isUser ? color : 'var(--tm-surface)', color: isUser ? '#fff' : 'var(--tm-text)', fontSize: 13, lineHeight: 1.5, wordBreak: 'break-word' }}>
        {msg.pending && !msg.text ? <span style={{ opacity: 0.5 }}>thinking…</span> : isUser ? <span dir="auto" style={{ whiteSpace: 'pre-wrap' }}>{msg.text}</span> : <div dir="auto"><MarkdownText text={msg.text} /></div>}
      </div>
      <div style={{ height: 24, display: 'flex', alignItems: 'center', paddingTop: 2, opacity: showActions ? 1 : 0, transition: 'opacity 0.12s', pointerEvents: showActions ? 'auto' : 'none' }}>
        <button
          onClick={handleCopy}
          title={copied ? 'Copied!' : 'Copy'}
          style={{
            width: 24, height: 24, border: 'none', borderRadius: 6, background: 'transparent',
            color: copied ? '#10b981' : 'var(--tm-text-muted)', cursor: 'pointer',
            display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 0,
            transition: 'color 0.15s, background 0.15s',
          }}
          onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.background = 'var(--tm-surface)'; }}
          onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.background = 'transparent'; }}
        >
          {copied ? (
            <svg width="13" height="13" viewBox="0 0 12 12" fill="none" xmlns="http://www.w3.org/2000/svg">
              <path d="M2 6l3 3 5-5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
            </svg>
          ) : (
            <svg width="13" height="13" viewBox="0 0 12 12" fill="none" xmlns="http://www.w3.org/2000/svg">
              <rect x="4" y="1" width="7" height="8" rx="1.5" stroke="currentColor" strokeWidth="1.2"/>
              <path d="M1 4h2v6a1 1 0 001 1h5v1.5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/>
            </svg>
          )}
        </button>
      </div>
    </div>
  );
}
