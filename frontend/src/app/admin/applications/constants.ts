import type { LogoState, LogoStateDef, CanvasRule } from './types';
import type { Node, Edge } from '@xyflow/react';
import type { EntryPointData, OrchestratorData } from './types';

// ── Design tokens ─────────────────────────────────────────────────────────────
export const C = {
  bg: 'var(--tm-bg)',
  surface: 'var(--tm-panel)',
  surfaceContainer: 'var(--tm-canvas-container)',
  surfaceLow: 'var(--tm-canvas-inset)',
  cyan: '#00f0ff',
  cyanBg: 'rgba(0,240,255,0.05)',
  cyanBorder: 'rgba(0,240,255,0.4)',
  cyanGlow: '0 0 15px rgba(0,240,255,0.15)',
  purple: '#d0bcff',
  purpleBg: 'rgba(87,27,193,0.1)',
  purpleBorder: '#d0bcff',
  purpleGlow: '0 0 15px rgba(208,188,255,0.15)',
  green: '#4ade80',
  greenBg: 'rgba(74,222,128,0.05)',
  greenBorder: 'rgba(74,222,128,0.3)',
  amber: '#f59e0b',
  amberBg: 'rgba(245,158,11,0.05)',
  amberBorder: 'rgba(245,158,11,0.3)',
  amberGlow: '0 0 15px rgba(245,158,11,0.15)',
  text: 'var(--tm-card-text)',
  textMuted: 'var(--tm-card-text-muted)',
  outline: 'var(--tm-canvas-border)',
  outlineVariant: 'var(--tm-canvas-border)',
  error: '#ffb4ab',
  errorBg: 'rgba(255,180,171,0.1)',
  glass: 'var(--tm-panel)',
  glassBorder: 'var(--tm-canvas-glass-border)',
};

export const glass = {
  background: C.glass,
  backdropFilter: 'blur(12px)',
  WebkitBackdropFilter: 'blur(12px)',
  border: `1px solid ${C.glassBorder}`,
};

// ── Entry point types ─────────────────────────────────────────────────────────
export const ENTRY_POINT_TYPES = ['websocket', 'sse', 'webrtc', 'a2a', 'voice'] as const;

// ── Proposal allowed fields ───────────────────────────────────────────────────
export const PROPOSAL_ALLOWED_FIELDS = new Set([
  'system_prompt', 'description', 'display_name',
  'max_iterations', 'history_window', 'max_parallel_tools',
]);

// ── Canvas CSS ────────────────────────────────────────────────────────────────
export const CANVAS_STYLES = `
  /* Force all text bright — builder lives on a dark bg, globals.css light-mode vars bleed in */
  .builder-root, .builder-root * {
    color: inherit;
  }
  .builder-root input,
  .builder-root select,
  .builder-root textarea {
    color: var(--tm-card-text) !important;
    background-color: var(--tm-canvas-inset) !important;
    -webkit-text-fill-color: var(--tm-card-text) !important;
  }
  .builder-root input::placeholder,
  .builder-root textarea::placeholder {
    color: var(--tm-card-text-muted) !important;
    -webkit-text-fill-color: var(--tm-card-text-muted) !important;
  }
  .builder-root input[style*="color: #f59e0b"],
  .builder-root input[style*="color:#f59e0b"] {
    color: #f59e0b !important;
    -webkit-text-fill-color: #f59e0b !important;
  }
  /* Slug input on entry-point node */
  .ep-slug-set {
    color: var(--tm-card-text) !important;
    -webkit-text-fill-color: var(--tm-card-text) !important;
  }
  .ep-slug-missing {
    color: #f59e0b !important;
    -webkit-text-fill-color: #f59e0b !important;
  }
  .ep-slug-missing::placeholder {
    color: rgba(245,158,11,0.5) !important;
    -webkit-text-fill-color: rgba(245,158,11,0.5) !important;
  }
  @keyframes handlePulse {
    0%, 100% { box-shadow: 0 0 0 0 rgba(0,240,255,0.4); }
    50% { box-shadow: 0 0 0 5px rgba(0,240,255,0); }
  }
  @keyframes nodeShake {
    0%, 100% { transform: translateX(0); }
    15% { transform: translateX(-6px); }
    30% { transform: translateX(6px); }
    45% { transform: translateX(-4px); }
    60% { transform: translateX(4px); }
    75% { transform: translateX(-2px); }
    90% { transform: translateX(2px); }
  }
  .node-error-ring {
    outline: 2px solid rgba(248,113,113,0.85) !important;
    outline-offset: 3px;
    border-radius: 50% !important;
    box-shadow: 0 0 14px rgba(248,113,113,0.45) !important;
  }
  .node-shake {
    animation: nodeShake 0.6s ease-in-out;
  }
  .react-flow__node.selected .react-flow__handle {
    animation: handlePulse 1.2s ease-in-out infinite;
  }
  .react-flow__node.selected {
    outline: none !important;
    box-shadow: none !important;
  }
  .react-flow__node *:not(.material-symbols-outlined):not(.material-icons) {
    font-family: inherit;
    box-sizing: border-box;
  }
  .react-flow__node .material-symbols-outlined {
    font-family: 'Material Symbols Outlined';
    box-sizing: border-box;
  }
  .react-flow__edge:hover .react-flow__edge-path {
    stroke-width: 3;
    filter: drop-shadow(0 0 4px rgba(248,113,113,0.6));
    cursor: pointer;
  }
  .react-flow__edge-path {
    cursor: pointer;
  }
  /* Tooltip */
  .nl-tooltip {
    position: relative;
  }
  .nl-tooltip .nl-tip {
    display: none;
    position: absolute;
    left: calc(100% + 10px);
    top: 50%;
    transform: translateY(-50%);
    background: var(--tm-card-chrome);
    border: 1px solid rgba(0,240,255,0.22);
    border-radius: 8px;
    padding: 8px 11px;
    width: 220px;
    z-index: 999;
    pointer-events: none;
    box-shadow: 0 8px 24px rgba(0,0,0,0.5);
  }
  .nl-tooltip:hover .nl-tip {
    display: block;
  }
  /* Section subscroll */
  .nl-section-list {
    max-height: 430px; /* ~10 items × 43px */
    overflow-y: auto;
    overflow-x: hidden;
    scrollbar-width: thin;
    scrollbar-color: rgba(0,240,255,0.2) transparent;
  }
  .nl-section-list::-webkit-scrollbar { width: 4px; }
  .nl-section-list::-webkit-scrollbar-track { background: transparent; }
  .nl-section-list::-webkit-scrollbar-thumb { background: rgba(0,240,255,0.2); border-radius: 4px; }
  .nl-section-list::-webkit-scrollbar-thumb:hover { background: rgba(0,240,255,0.4); }
  .comp-panel { scrollbar-width: thin; scrollbar-color: rgba(255,255,255,0.1) transparent; }
  .comp-panel::-webkit-scrollbar { width: 4px; }
  .comp-panel::-webkit-scrollbar-track { background: transparent; }
  .comp-panel::-webkit-scrollbar-thumb { background: rgba(255,255,255,0.1); border-radius: 4px; }
  .comp-panel::-webkit-scrollbar-thumb:hover { background: rgba(255,255,255,0.22); }
`;

// ── Custom animated edge ──────────────────────────────────────────────────────
export const EDGE_STYLE = {
  stroke: C.cyan,
  strokeWidth: 1.5,
  strokeDasharray: '5,3',
};

// ── Dagre layout ──────────────────────────────────────────────────────────────
export const NODE_WIDTH = 240;
export const NODE_HEIGHT = 80;

// ── Internal orchestrator names ───────────────────────────────────────────────
export const INTERNAL_ORCHESTRATOR_NAMES = new Set(['workflow_advisor']);

// ── EP metadata ───────────────────────────────────────────────────────────────
export const EP_META: Record<string, { emoji: string; title: string; desc: string; color?: string }> = {
  websocket: { emoji: '⚡', title: 'WebSocket', desc: 'Full-duplex, persistent connection. Client and server can send messages at any time. Best for chat, real-time collaboration, and interactive agents.' },
  sse:       { emoji: '📡', title: 'Server-Sent Events', desc: 'One-way server→client stream over HTTP. Lightweight, works through proxies. Best for dashboards, notifications, and read-only agent output.' },
  webrtc:    { emoji: '🎙️', title: 'WebRTC Voice', desc: 'Real-time voice via LiveKit WebRTC. Low-latency bidirectional audio with automatic voice activity detection. Best for voice assistants and spoken-word agents.', color: '#a78bfa' },
  a2a:       { emoji: '🤖', title: 'A2A External', desc: 'Expose this orchestrator as an A2A agent for external callers. The A2A skill id is the entry point slug. Best for machine-to-machine orchestration.', color: '#f59e0b' },
  voice:     { emoji: '🎤', title: 'Voice (STT/TTS)', desc: 'Speech-to-speech over HTTP. Browser sends audio → STT → orchestrator → TTS → audio reply. Requires STT + TTS config on the connected orchestrator.', color: '#f59e0b' },
};

// ── Canvas v2 LLM provider/model map ─────────────────────────────────────────
export const MODELS_BY_PROVIDER: Record<string, string[]> = {
  anthropic: ['claude-opus-4-8', 'claude-sonnet-4-6', 'claude-haiku-4-5-20251001'],
  openai:    ['gpt-4o', 'gpt-4o-mini', 'gpt-4-turbo', 'o1', 'o3-mini'],
  groq:      ['openai/gpt-oss-120b', 'openai/gpt-oss-20b', 'qwen/qwen3.6-27b', 'groq/compound', 'groq/compound-mini'],
  gemini:    ['gemini-2.5-pro', 'gemini-2.0-flash', 'gemini-1.5-pro'],
};

export const PROVIDER_OPTIONS = ['anthropic', 'openai', 'groq', 'gemini'];

// ── App card styles ───────────────────────────────────────────────────────────
export const APP_CARD_STYLES = `
.app-glass-card {
  background:
    linear-gradient(160deg, rgba(255,255,255,0.032) 0%, rgba(255,255,255,0.006) 40%, rgba(0,0,0,0.06) 100%),
    var(--tm-card);
  border: 1px solid var(--tm-card-border);
  backdrop-filter: blur(12px);
  box-shadow:
    0 8px 32px rgba(0,0,0,0.4),
    0 2px 8px rgba(0,0,0,0.25),
    inset 0 1px 0 rgba(255,255,255,0.04);
  transition: border-color 240ms ease, box-shadow 240ms ease;
}
.app-glass-card:hover {
  border-color: rgba(0,209,255,0.28);
  box-shadow:
    0 8px 32px rgba(0,0,0,0.5),
    0 2px 8px rgba(0,0,0,0.28),
    0 0 0 1px rgba(0,209,255,0.1),
    0 0 32px rgba(0,209,255,0.08),
    inset 0 1px 0 rgba(255,255,255,0.055);
}
.app-glass-card:active {
  box-shadow:
    0 4px 16px rgba(0,0,0,0.5),
    inset 0 1px 0 rgba(255,255,255,0.03);
  border-color: rgba(0,209,255,0.4);
  transition: border-color 80ms ease, box-shadow 80ms ease;
}
.app-card-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  padding: 9px 4px;
  border-radius: 8px;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.01em;
  cursor: pointer;
  transition: border-color 180ms ease, background 180ms ease,
              box-shadow 180ms ease, transform 180ms ease;
  white-space: nowrap;
}
.app-card-btn--open {
  background: #00d1ff;
  color: #021520;
  border: none;
  box-shadow: 0 0 14px rgba(0,209,255,0.38);
}
.app-card-btn--open:hover {
  background: #22dcff;
  box-shadow: 0 0 22px rgba(0,209,255,0.55);
}
.app-card-btn--open:active {
  background: #00b8e0;
  box-shadow: 0 0 10px rgba(0,209,255,0.3);
}
.app-card-btn--urls {
  background: var(--tm-btn-2-bg);
  color: var(--tm-card-text-subtle);
  border: 1px solid var(--tm-btn-2-border);
}
.app-card-btn--urls:hover {
  border-color: rgba(129,140,248,0.45);
  color: #818cf8;
  background: rgba(99,102,241,0.1);
}
.app-card-btn--toggle-on {
  background: var(--tm-btn-2-bg);
  color: #f87171;
  border: 1px solid rgba(248,113,113,0.2);
}
.app-card-btn--toggle-on:hover {
  border-color: rgba(248,113,113,0.5);
  background: rgba(248,113,113,0.08);
}
.app-card-btn--toggle-off {
  background: var(--tm-btn-2-bg);
  color: #34d399;
  border: 1px solid rgba(52,211,153,0.2);
}
.app-card-btn--toggle-off:hover {
  border-color: rgba(52,211,153,0.5);
  background: rgba(52,211,153,0.08);
}
.app-deploy-card:hover {
  border-color: rgba(99,102,241,0.7) !important;
  background: rgba(99,102,241,0.04) !important;
}
`;

// ── EP icon/label for AppCard ─────────────────────────────────────────────────
export const EP_ICON: Record<string, string> = { websocket: 'bolt', sse: 'stream', webrtc: 'videocam', a2a: 'robot_2', voice: 'mic' };
export const EP_LABEL: Record<string, string> = { websocket: 'WebSocket', sse: 'SSE', webrtc: 'WebRTC', a2a: 'A2A' };

// ── RuntimeView provider list ─────────────────────────────────────────────────
export const PROVIDER_LIST = ['anthropic', 'openai', 'groq', 'gemini', 'elevenlabs'] as const;

export const RUNTIME_MODELS: Record<string, string[]> = {
  anthropic: ['claude-opus-4-8', 'claude-sonnet-4-6', 'claude-haiku-4-5-20251001'],
  openai:    ['gpt-4o', 'gpt-4o-mini', 'gpt-4-turbo'],
  groq:      ['llama3-70b-8192', 'llama3-8b-8192', 'mixtral-8x7b-32768'],
  gemini:    ['gemini-2.5-pro', 'gemini-2.0-flash', 'gemini-1.5-flash'],
};

// Voice (STT / TTS) constants
export const STT_PROVIDERS = ['openai', 'groq'] as const;
export const STT_MODELS: Record<string, string[]> = {
  openai: ['whisper-1'],
  groq:   ['whisper-large-v3', 'whisper-large-v3-turbo'],
};

export const TTS_PROVIDERS = ['openai', 'elevenlabs'] as const;
export const TTS_VOICES_OPENAI = ['alloy', 'echo', 'fable', 'onyx', 'nova', 'shimmer'] as const;
export const TTS_MODELS_OPENAI = ['tts-1', 'tts-1-hd'] as const;

// ── Sessions styles ───────────────────────────────────────────────────────────
export const SESSIONS_STYLES = `
@keyframes sess-pulse {
  0%,100% { opacity:1; transform:scale(1); }
  50%      { opacity:0.7; transform:scale(1.08); }
}
@keyframes edge-dash-cyan {
  to { stroke-dashoffset: -24; }
}
@keyframes edge-dash-purple {
  to { stroke-dashoffset: -20; }
}
@keyframes edge-glow-cyan {
  0%,100% { filter: drop-shadow(0 0 3px rgba(0,240,255,0.55)) drop-shadow(0 0 7px rgba(0,240,255,0.28)); }
  50%     { filter: drop-shadow(0 0 7px rgba(0,240,255,1.0)) drop-shadow(0 0 16px rgba(0,240,255,0.55)); }
}
@keyframes edge-glow-purple {
  0%,100% { filter: drop-shadow(0 0 3px rgba(208,188,255,0.5)) drop-shadow(0 0 7px rgba(208,188,255,0.25)); }
  50%     { filter: drop-shadow(0 0 7px rgba(208,188,255,0.95)) drop-shadow(0 0 16px rgba(208,188,255,0.5)); }
}
.react-flow__edge.active-ep-orch path.react-flow__edge-path {
  stroke-dasharray: 8 4;
  animation: edge-dash-cyan 0.5s linear infinite, edge-glow-cyan 1.8s ease-in-out infinite;
}
.react-flow__edge.active-orch-agent path.react-flow__edge-path {
  stroke-dasharray: 6 4;
  animation: edge-dash-purple 0.65s linear infinite, edge-glow-purple 1.8s ease-in-out infinite;
}
.sess-badge {
  position:absolute; top:-8px; right:-8px;
  min-width:20px; height:20px; border-radius:10px;
  background:rgba(0,240,255,0.15); border:1.5px solid rgba(0,240,255,0.55);
  color:#00f0ff; font-size:10px; font-weight:700;
  display:flex; align-items:center; justify-content:center; padding:0 4px;
  font-family:Inter,sans-serif; pointer-events:none; z-index:20;
  box-shadow:0 0 8px rgba(0,240,255,0.3);
}
.sess-badge.active { animation:sess-pulse 1.8s ease-in-out infinite; }
.sess-row {
  display:flex; align-items:flex-start; gap:10px;
  padding:10px 14px; border-radius:10px; cursor:pointer;
  border:1px solid transparent; transition:all 0.15s ease;
}
.sess-row:hover { background:rgba(0,240,255,0.04); border-color:rgba(0,240,255,0.18); }
.sess-row.selected { background:rgba(0,240,255,0.07); border-color:rgba(0,240,255,0.35); }
`;

// ── Logo ──────────────────────────────────────────────────────────────────────
export const LOGO_STATES: Record<LogoState, LogoStateDef> = {
  idle:     { opacity: 0.015, filter: 'none',   animation: 'none' },
  dirty:    { opacity: 0.015, filter: 'none',   animation: 'none' },
  warning:  { opacity: 0.45, filter: 'drop-shadow(0 0 18px rgba(255,120,120,0.4))',    animation: 'logo-warn-flash 1.2s ease-in-out 1 forwards' },
  error:    { opacity: 0.35, filter: 'drop-shadow(0 0 18px rgba(255,107,138,0.4))',   animation: 'logo-shake 0.5s ease-in-out' },
  success:  { opacity: 1.0,  filter: 'drop-shadow(0 0 40px rgba(74,222,128,0.9))',    animation: 'logo-burst 1.8s ease-out forwards' },
  thinking: { opacity: 1.0,  filter: 'none',                                           animation: 'none' },
};

export const LOGO_KEYFRAMES = `
@keyframes logo-breathe {
  0%   { opacity: 0.012; }
  50%  { opacity: 0.028; }
  100% { opacity: 0.012; }
}
@keyframes logo-breathe-v3 {
  0%   { opacity: 0.007; }
  50%  { opacity: 0.015; }
  100% { opacity: 0.007; }
}
@keyframes logo-sway {
  0%, 100% { transform: rotate3d(0,1,0,0deg); }
  25%       { transform: rotate3d(0,1,0,6deg); }
  75%       { transform: rotate3d(0,1,0,-6deg); }
}
@keyframes logo-shake {
  0%,100% { transform: translateX(0); }
  15%     { transform: translateX(-10px) rotate(-2deg); }
  30%     { transform: translateX(10px)  rotate(2deg); }
  45%     { transform: translateX(-8px)  rotate(-1deg); }
  60%     { transform: translateX(8px)   rotate(1deg); }
  75%     { transform: translateX(-4px); }
  90%     { transform: translateX(4px); }
}
@keyframes logo-burst {
  0%   { opacity: 0.13; filter: drop-shadow(0 0 18px rgba(0,240,255,0.18)); }
  15%  { opacity: 1;    filter: drop-shadow(0 0 80px rgba(74,222,128,1)) drop-shadow(0 0 40px rgba(255,255,255,0.8)); }
  100% { opacity: 0.13; filter: drop-shadow(0 0 18px rgba(0,240,255,0.18)); }
}
@keyframes logo-explode {
  0%   { transform: translate(0,0) scale(1) rotate(0deg);                                              opacity: 1; }
  20%  { transform: translate(calc(var(--ex)*60px), calc(var(--ey)*60px)) scale(1.15) rotate(var(--rot)); opacity: 1; }
  55%  { transform: translate(calc(var(--ex)*140px), calc(var(--ey)*140px)) scale(0.7) rotate(calc(var(--rot)*2)); opacity: 0.6; }
  80%  { transform: translate(calc(var(--ex)*180px), calc(var(--ey)*180px)) scale(0.3) rotate(calc(var(--rot)*3)); opacity: 0.0; }
  81%  { transform: translate(0,0) scale(0) rotate(0deg);                                              opacity: 0; }
  100% { transform: translate(0,0) scale(1) rotate(0deg);                                              opacity: 1; }
}
@keyframes logo-flip {
  0%   { transform: perspective(600px) rotateY(0deg); }
  100% { transform: perspective(600px) rotateY(360deg); }
}
@keyframes logo-polygon-flicker {
  0%,100% { opacity: 0.08; fill: #4ab8a0; }
  50%     { opacity: 0.55; fill: #00b8c8; filter: drop-shadow(0 0 6px rgba(0,180,200,0.6)); }
}
@keyframes logo-warn-flash {
  0%   { opacity: 0.18; filter: drop-shadow(0 0 12px rgba(255,120,120,0.15)); }
  40%  { opacity: 0.48; filter: drop-shadow(0 0 22px rgba(255,120,120,0.5)); }
  100% { opacity: 0.18; filter: drop-shadow(0 0 12px rgba(255,120,120,0.15)); }
}
`;

export const LOGO_PATHS: Array<{ id: string; points: string; ex: number; ey: number }> = [
  { id: 'part-01', ex: -0.5, ey: -1.0, points: "88,77 184,146 244,191 281,217 336,259 355,272 358,272 367,267 372,266 379,262 391,258 433,239 440,237 473,222 513,206 520,202 546,192 555,187 558,187 446,102 433,91 421,83 403,68 397,65 392,60 331,15 318,4 274,19 264,21 246,28 239,29 217,37 214,37 211,39 201,41 189,46 186,46 154,57 151,57 148,59 141,60 138,62 104,73 101,73 98,75" },
  { id: 'part-02', ex:  0.5, ey: -1.0, points: "1323,77 1313,75 1292,67 1289,67 1239,50 1236,50 1233,48 1230,48 1189,34 1176,31 1094,4 1085,12 1074,19 1053,36 959,106 855,187 876,196 881,197 973,237 980,239 1034,263 1048,268 1055,272 1059,272 1139,213 1146,209 1177,185 1188,178 1208,162 1284,107" },
  { id: 'part-03', ex: -1.2, ey:  0.1, points: "70,97 70,334 71,335 72,350 76,365 104,429 108,435 180,486 184,490 245,534 345,609 339,293 305,269 281,250 252,230 182,177 179,176 153,156 150,155" },
  { id: 'part-04', ex:  1.2, ey:  0.1, points: "1342,97 1252,162 1248,166 1152,236 1148,240 1126,255 1122,259 1112,265 1103,273 1074,293 1073,296 1073,317 1072,318 1072,355 1071,356 1071,415 1070,416 1070,461 1069,462 1069,526 1068,527 1067,609 1306,433 1325,392 1336,365 1341,343 1341,331 1342,330" },
  { id: 'part-05', ex: -0.4, ey: -0.6, points: "682,361 576,210 381,292 532,410 577,395 580,395 586,392 595,390 613,384 616,382 622,381 664,367 667,365" },
  { id: 'part-06', ex:  0.4, ey: -0.6, points: "732,361 803,384 806,386 809,386 831,394 834,394 860,404 863,404 881,410 1033,291 837,210 764,315 760,319 759,322 740,348" },
  { id: 'part-07', ex: -0.8, ey:  0.0, points: "367,314 367,373 368,374 368,430 369,431 371,567 380,574 383,575 388,580 396,585 505,669 508,611 509,610 509,595 510,594 512,540 513,539 513,524 514,523 514,504 515,503 515,490 516,489 517,454 518,453 518,434 519,433 504,421 501,420 490,410 468,394 427,361 423,359 395,336 392,335" },
  { id: 'part-08', ex:  0.8, ey:  0.0, points: "1046,314 894,433 895,456 896,457 896,475 897,476 897,494 898,495 898,513 899,514 901,561 902,562 902,579 903,580 903,594 904,595 906,650 907,651 907,666 908,669 934,648 971,621 1041,567" },
  { id: 'part-09', ex: -0.3, ey: -0.3, points: "549,424 693,539 693,534 694,533 693,532 693,377 676,382 664,387 660,387 657,389 654,389 635,396 632,396" },
  { id: 'part-10', ex:  0.3, ey: -0.3, points: "864,424 815,407 812,407 791,400 779,395 776,395 721,377 721,539 732,529 736,527 752,513" },
  { id: 'part-11', ex: -0.4, ey:  0.5, points: "535,446 532,511 531,512 531,531 530,532 530,546 529,547 529,567 528,568 527,600 526,601 526,616 525,617 525,634 524,635 524,650 523,651 523,662 522,663 522,682 543,697 628,763 640,771 645,776 649,778 692,812 693,809 693,799 692,798 692,793 693,792 693,572 685,567 679,561 675,559 649,537 611,508 605,502 602,501" },
  { id: 'part-12', ex:  0.4, ey:  0.5, points: "878,446 721,572 721,594 720,595 720,775 721,776 720,780 721,781 721,812 752,787 756,785 816,738 892,681 891,669 890,668 890,650 889,649 889,633 888,632 888,619 887,618 885,567 884,566 884,554 883,553 883,532 882,531 882,513 881,512" },
  { id: 'part-13', ex: -1.3, ey:  0.8, points: "100,461 95,488 89,506 87,509 86,515 77,534 75,541 55,582 38,613 16,647 13,656 13,662 16,670 26,679 42,685 62,690 67,693 74,700 76,705 76,720 68,743 68,749 70,755 75,763 87,770 97,772 125,772 126,771 130,772 128,775 112,781 89,784 83,791 81,797 81,805 83,811 89,818 100,824 105,829 109,836 111,843 111,860 105,889 105,910 108,922 115,933 121,939 173,974 286,1057 326,1088 345,1105 345,641" },
  { id: 'part-14', ex:  1.3, ey:  0.8, points: "1312,462 1273,489 1230,522 1227,523 1143,586 1067,641 1067,1106 1080,1093 1135,1050 1138,1049 1172,1023 1235,978 1239,974 1249,968 1253,964 1256,963 1270,952 1292,938 1301,928 1305,920 1307,912 1308,897 1307,896 1307,888 1301,858 1302,839 1307,829 1312,824 1323,818 1328,813 1331,806 1331,796 1330,792 1324,784 1311,783 1297,780 1286,776 1282,773 1284,771 1287,772 1316,772 1328,769 1335,765 1340,760 1344,750 1344,742 1336,717 1336,706 1339,699 1344,694 1353,689 1371,685 1386,679 1394,673 1399,663 1399,655 1397,649 1372,610 1339,546 1321,500 1321,497 1316,484" },
];

export const LOGO_COLOR = '#a0f0d0';

// Stable per-polygon random delays for the thinking flicker — generated once at module load
export const THINK_DELAYS = LOGO_PATHS.map((_, i) => {
  const r = ((i * 2654435761) >>> 0) / 0xffffffff;
  return +(r * 2.4).toFixed(2);
});
export const THINK_DURATIONS = LOGO_PATHS.map((_, i) => {
  const r = (((i + 7) * 2246822519) >>> 0) / 0xffffffff;
  return +(0.9 + r * 1.4).toFixed(2);
});

// ── Advisor field labels/icons ────────────────────────────────────────────────
export const FIELD_LABEL: Record<string, string> = {
  system_prompt: 'System prompt', description: 'Description',
  display_name: 'Display name', max_iterations: 'Max iterations',
  history_window: 'History window', max_parallel_tools: 'Max parallel tools',
};

export const FIELD_ICON: Record<string, string> = {
  system_prompt: 'edit_note', description: 'description', display_name: 'label',
  max_iterations: 'repeat', history_window: 'history', max_parallel_tools: 'fork_right',
};

// ── Canvas toolbar button style ───────────────────────────────────────────────
export const toolBtnStyle: React.CSSProperties = {
  padding: '4px 8px', borderRadius: 6, border: 'none', cursor: 'pointer',
  background: 'transparent', color: C.textMuted, display: 'flex', alignItems: 'center',
  transition: 'all 0.1s',
};

// ── Node port definitions ─────────────────────────────────────────────────────
export const NODE_PORTS: Record<string, { accepts: string[]; emits: string[]; maxOutgoing?: number; maxIncoming?: number }> = {
  entryPoint:   { accepts: [],                           emits: ['request'] },
  orchestrator: { accepts: ['request', 'signal'],         emits: ['task', 'signal'] },
  agent:        { accepts: ['task', 'mw_task'],           emits: ['result'] },
  middleware:   { accepts: ['task', 'mw_task'],           emits: ['mw_task'] },
};

// ── Canvas rules ──────────────────────────────────────────────────────────────
export const CANVAS_RULES: CanvasRule[] = [
  {
    id: 'AT_LEAST_ONE_EP',
    severity: 'block',
    message: ({ nodes }: { nodes: Node[]; edges: Edge[] }) => nodes.filter((n: Node) => n.type === 'entryPoint').length === 0
      ? 'Drop an Entry Point to start' : null,
  },
  {
    id: 'EP_SLUG_NONEMPTY',
    severity: 'block',
    message: ({ nodes }: { nodes: Node[]; edges: Edge[] }) => {
      const bad = nodes.filter((n: Node) => n.type === 'entryPoint' && !(n.data as EntryPointData).slug);
      return bad.length > 0 ? 'Every entry point needs a slug' : null;
    },
    errorNodeIds: ({ nodes }: { nodes: Node[]; edges: Edge[] }) =>
      nodes.filter((n: Node) => n.type === 'entryPoint' && !(n.data as EntryPointData).slug).map((n: Node) => n.id),
  },
  {
    id: 'EP_SLUG_UNIQUE',
    severity: 'block',
    message: ({ nodes }: { nodes: Node[]; edges: Edge[] }) => {
      const slugs = nodes.filter((n: Node) => n.type === 'entryPoint').map((n: Node) => (n.data as EntryPointData).slug ?? '').filter((s: string) => s !== '');
      return new Set(slugs).size !== slugs.length ? 'Duplicate entry point slug — each slug must be unique' : null;
    },
    errorNodeIds: ({ nodes }: { nodes: Node[]; edges: Edge[] }) => {
      const seen = new Set<string>(); const dupes = new Set<string>();
      nodes.filter((n: Node) => n.type === 'entryPoint').forEach((n: Node) => {
        const s = (n.data as EntryPointData).slug ?? '';
        if (s) { if (seen.has(s)) dupes.add(s); else seen.add(s); }
      });
      return nodes.filter((n: Node) => n.type === 'entryPoint' && dupes.has((n.data as EntryPointData).slug ?? '')).map((n: Node) => n.id);
    },
  },
  {
    id: 'EP_SLUG_FORMAT',
    severity: 'block',
    message: ({ nodes }: { nodes: Node[]; edges: Edge[] }) => {
      const bad = nodes.filter((n: Node) => n.type === 'entryPoint' && !(n.data as EntryPointData).slug?.match(/^[a-z0-9_-]{1,64}$/));
      return bad.length > 0 ? `Slug "${(bad[0].data as EntryPointData).slug}": lowercase letters, numbers, _ or - only` : null;
    },
    errorNodeIds: ({ nodes }: { nodes: Node[]; edges: Edge[] }) =>
      nodes.filter((n: Node) => n.type === 'entryPoint' && !(n.data as EntryPointData).slug?.match(/^[a-z0-9_-]{1,64}$/)).map((n: Node) => n.id),
  },
  {
    id: 'EP_HAS_ORCH',
    severity: 'block',
    message: ({ nodes, edges }: { nodes: Node[]; edges: Edge[] }) => {
      const epNodes = nodes.filter((n: Node) => n.type === 'entryPoint');
      const unconnected = epNodes.filter((ep: Node) => !edges.some((e: Edge) => e.source === ep.id && nodes.find((n: Node) => n.id === e.target && n.type === 'orchestrator')));
      return unconnected.length > 0 ? 'Every entry point must connect to an orchestrator' : null;
    },
    errorNodeIds: ({ nodes, edges }: { nodes: Node[]; edges: Edge[] }) =>
      nodes.filter((n: Node) => n.type === 'entryPoint' && !edges.some((e: Edge) => e.source === n.id && nodes.find((m: Node) => m.id === e.target && m.type === 'orchestrator'))).map((n: Node) => n.id),
  },
  {
    id: 'ORCH_HAS_AGENT',
    severity: 'warn',
    message: ({ nodes, edges }: { nodes: Node[]; edges: Edge[] }) => {
      const orchNodes = nodes.filter((n: Node) => n.type === 'orchestrator');
      const empty = orchNodes.filter((o: Node) => !edges.some((e: Edge) => e.source === o.id && nodes.find((n: Node) => n.id === e.target && n.type === 'agent')));
      return empty.length > 0 ? `${empty.length} orchestrator${empty.length > 1 ? 's have' : ' has'} no agents` : null;
    },
    errorNodeIds: ({ nodes, edges }: { nodes: Node[]; edges: Edge[] }) =>
      nodes.filter((n: Node) => n.type === 'orchestrator' && !edges.some((e: Edge) => e.source === n.id && nodes.find((m: Node) => m.id === e.target && m.type === 'agent'))).map((n: Node) => n.id),
  },
  {
    id: 'VOICE_EP_NEEDS_STT_TTS',
    severity: 'warn',
    message: ({ nodes, edges }: { nodes: Node[]; edges: Edge[] }) => {
      const voiceEps = nodes.filter((n: Node) => n.type === 'entryPoint' && (n.data as EntryPointData).epType === 'voice');
      for (const ep of voiceEps) {
        const orchEdge = edges.find((e: Edge) => e.source === ep.id);
        if (!orchEdge) continue;
        const orch = nodes.find((n: Node) => n.id === orchEdge.target && n.type === 'orchestrator');
        if (!orch) continue;
        const d = orch.data as OrchestratorData;
        if (!d.transcriptionProvider || !d.ttsProvider) {
          return `Voice entry point requires STT and TTS providers configured on its orchestrator`;
        }
      }
      return null;
    },
    errorNodeIds: ({ nodes, edges }: { nodes: Node[]; edges: Edge[] }) => {
      const bad: string[] = [];
      const voiceEps = nodes.filter((n: Node) => n.type === 'entryPoint' && (n.data as EntryPointData).epType === 'voice');
      for (const ep of voiceEps) {
        const orchEdge = edges.find((e: Edge) => e.source === ep.id);
        if (!orchEdge) continue;
        const orch = nodes.find((n: Node) => n.id === orchEdge.target && n.type === 'orchestrator');
        if (!orch) continue;
        const d = orch.data as OrchestratorData;
        if (!d.transcriptionProvider || !d.ttsProvider) bad.push(ep.id, orch.id);
      }
      return bad;
    },
  },
];

// ── Monitoring defaults ───────────────────────────────────────────────────────
export const MON_DEFAULTS = {
  heatmap_low: 1, heatmap_medium: 10, heatmap_high: 50,
  edge_thin: 1, edge_medium: 10, edge_thick: 50,
  panel_max_sessions: 50, stats_window_seconds: 300,
};
