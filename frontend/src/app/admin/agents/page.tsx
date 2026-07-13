'use client';
import { useEffect, useRef, useState } from 'react';
import Sidebar from '@/components/Sidebar';
import AuthGuard from '@/components/AuthGuard';
import { themApi, type Agent, type AgentSkill, type DiscoverResult, type OrchestratorFull, type ScanResult } from '@/lib/api';

const EMPTY_FORM = {
  slug: '',
  display_name: '',
  description: '',
  transport: 'a2a_async',
  endpoint_url: '',
  auth_token: '',
  max_concurrency: 3,
  timeout_seconds: 60,
  enabled: true,
  skills: [] as AgentSkill[],
  supports_streaming: false,
  supports_push: false,
  icon: '',
  agent_card: null as Record<string, unknown> | null,
  agent_card_url: '',
};

type FormState = typeof EMPTY_FORM;

// ── Diff helpers ──────────────────────────────────────────────────────────────

type CardDiff = {
  hasChanges: boolean;
  displayName: { old: string; new: string; changed: boolean };
  description: { old: string; new: string; changed: boolean };
  skills: { old: AgentSkill[]; new: AgentSkill[]; changed: boolean };
  streaming: { old: boolean; new: boolean; changed: boolean };
  push: { old: boolean; new: boolean; changed: boolean };
  version: { old: string; new: string; changed: boolean };
  provider: { old: string; new: string; changed: boolean };
};

function buildDiff(agent: Agent, result: DiscoverResult): CardDiff {
  const oldCard = (agent.agent_card ?? {}) as Record<string, unknown>;
  const newCard = (result.agent_card ?? {}) as Record<string, unknown>;

  const oldVersion = String(oldCard.version ?? '');
  const newVersion = String(newCard.version ?? '');

  const oldProvider = typeof oldCard.provider === 'object' && oldCard.provider
    ? String((oldCard.provider as Record<string, unknown>).organization ?? '')
    : '';
  const newProvider = typeof newCard.provider === 'object' && newCard.provider
    ? String((newCard.provider as Record<string, unknown>).organization ?? '')
    : '';

  const oldSkillsJson = JSON.stringify((agent.skills ?? []).map(s => ({ id: s.id, name: s.name, description: s.description ?? '', tags: (s.tags ?? []).sort() })));
  const newSkillsJson = JSON.stringify((result.skills ?? []).map(s => ({ id: s.id, name: s.name, description: s.description ?? '', tags: (s.tags ?? []).sort() })));

  const fields = {
    displayName: { old: agent.display_name, new: result.display_name, changed: agent.display_name !== result.display_name },
    description: { old: agent.description, new: result.description, changed: agent.description !== result.description },
    skills: { old: agent.skills ?? [], new: result.skills, changed: oldSkillsJson !== newSkillsJson },
    streaming: { old: !!agent.supports_streaming, new: result.supports_streaming, changed: !!agent.supports_streaming !== result.supports_streaming },
    push: { old: !!agent.supports_push, new: result.supports_push, changed: !!agent.supports_push !== result.supports_push },
    version: { old: oldVersion, new: newVersion, changed: oldVersion !== newVersion },
    provider: { old: oldProvider, new: newProvider, changed: oldProvider !== newProvider },
  };

  const hasChanges = Object.values(fields).some(f => f.changed);
  return { hasChanges, ...fields };
}

// ── Utilities ─────────────────────────────────────────────────────────────────

function timeAgo(iso: string): string {
  const diffMs = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(diffMs / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

function cardVersion(card: Record<string, unknown> | null | undefined): string {
  return card ? String(card.version ?? '') : '';
}
function cardProvider(card: Record<string, unknown> | null | undefined): string {
  if (!card?.provider || typeof card.provider !== 'object') return '';
  return String((card.provider as Record<string, unknown>).organization ?? '');
}
function cardDocUrl(card: Record<string, unknown> | null | undefined): string {
  return card ? String(card.documentationUrl ?? '') : '';
}
function cardAuth(card: Record<string, unknown> | null | undefined): string[] {
  if (!card?.authentication || !Array.isArray(card.authentication)) return [];
  return (card.authentication as unknown[]).map(a => typeof a === 'string' ? a : String((a as Record<string, unknown>).scheme ?? a));
}

// ── Design tokens ─────────────────────────────────────────────────────────────

function riskColors(risk: 'low' | 'medium' | 'high') {
  if (risk === 'low') return {
    bg: 'linear-gradient(145deg, rgba(66,217,139,.14) 0%, rgba(42,181,109,.08) 100%)',
    border: 'rgba(66,217,139,.28)',
    color: '#4edea3',
    glow: 'rgba(42,181,109,.18)',
  };
  if (risk === 'medium') return {
    bg: 'linear-gradient(145deg, rgba(230,184,92,.14) 0%, rgba(180,131,9,.08) 100%)',
    border: 'rgba(230,184,92,.28)',
    color: '#e6b85c',
    glow: 'rgba(180,131,9,.18)',
  };
  return {
    bg: 'linear-gradient(145deg, rgba(220,38,38,.14) 0%, rgba(185,28,28,.08) 100%)',
    border: 'rgba(220,38,38,.28)',
    color: '#f87171',
    glow: 'rgba(220,38,38,.18)',
  };
}

function statusIcon(status: 'pass' | 'fail' | 'warn') {
  if (status === 'pass') return { icon: '✓', color: '#4edea3' };
  if (status === 'warn') return { icon: '⚠', color: '#e6b85c' };
  return { icon: '✗', color: '#f87171' };
}

function scoreRingColor(score: number) {
  if (score >= 75) return '#4edea3';
  if (score >= 45) return '#e6b85c';
  return '#f87171';
}

// ── Category / accent helpers ─────────────────────────────────────────────────

function agentCategory(agent: Agent): string {
  const slug = agent.slug.toLowerCase();
  const transport = agent.transport.toLowerCase();
  if (slug.includes('vision')) return 'Vision';
  if (slug.includes('security') || slug.includes('scanner')) return 'Security';
  if (slug.includes('debate') || slug.includes('judge') || slug.includes('evidence') || slug.includes('logic') || slug.includes('creative')) return 'Research';
  if (slug.includes('cod') || slug.includes('coder') || slug.includes('docu')) return 'Coding';
  if (transport === 'a2a' || transport === 'a2a_async') return 'A2A';
  const firstTag = (agent.skills?.[0]?.tags ?? [])[0];
  if (firstTag) return firstTag.charAt(0).toUpperCase() + firstTag.slice(1);
  return 'Agent';
}

function categoryBadgeStyle(category: string): React.CSSProperties {
  switch (category) {
    case 'A2A':      return { background: 'rgba(99,102,241,0.18)', color: '#818cf8', border: '1px solid rgba(99,102,241,0.3)' };
    case 'Research': return { background: 'rgba(168,85,247,0.18)', color: '#c084fc', border: '1px solid rgba(168,85,247,0.3)' };
    case 'Coding':   return { background: 'rgba(0,209,255,0.12)',  color: '#00d1ff', border: '1px solid rgba(0,209,255,0.28)' };
    case 'Vision':   return { background: 'rgba(59,130,246,0.18)', color: '#60a5fa', border: '1px solid rgba(59,130,246,0.3)' };
    case 'Security': return { background: 'rgba(245,158,11,0.15)', color: '#fbbf24', border: '1px solid rgba(245,158,11,0.28)' };
    default:         return { background: 'rgba(100,116,139,0.18)', color: '#94a3b8', border: '1px solid rgba(100,116,139,0.28)' };
  }
}

function categoryAccent(category: string): { color: string; glow: string; border: string } {
  switch (category) {
    case 'A2A':      return { color: '#818cf8', glow: 'rgba(99,102,241,0.25)',  border: 'rgba(99,102,241,0.45)' };
    case 'Coding':   return { color: '#00d1ff', glow: 'rgba(0,209,255,0.22)',   border: 'rgba(0,209,255,0.42)' };
    case 'Vision':   return { color: '#60a5fa', glow: 'rgba(59,130,246,0.22)',  border: 'rgba(59,130,246,0.42)' };
    case 'Research': return { color: '#c084fc', glow: 'rgba(168,85,247,0.22)',  border: 'rgba(168,85,247,0.42)' };
    case 'Security': return { color: '#fbbf24', glow: 'rgba(245,158,11,0.22)',  border: 'rgba(245,158,11,0.42)' };
    default:         return { color: '#94a3b8', glow: 'rgba(100,116,139,0.18)', border: 'rgba(100,116,139,0.35)' };
  }
}

// Unique icon per agent slug/category
function agentIcon(agent: Agent, category: string): string {
  const s = agent.slug.toLowerCase();
  if (s.includes('vision'))   return 'visibility';
  if (s.includes('security') || s.includes('scanner')) return 'security';
  if (s.includes('echo'))     return 'wifi_tethering';
  if (s.includes('slow'))     return 'hourglass_empty';
  if (s.includes('stream'))   return 'stream';
  if (s.includes('judge'))    return 'gavel';
  if (s.includes('debate'))   return 'forum';
  if (s.includes('evidence')) return 'fact_check';
  if (s.includes('logic'))    return 'psychology';
  if (s.includes('creative')) return 'auto_awesome';
  if (s.includes('docu'))     return 'description';
  if (s.includes('cod') || s.includes('coder')) return 'code';
  if (s.includes('research')) return 'biotech';
  if (s.includes('assistant')) return 'assistant';
  switch (category) {
    case 'A2A':      return 'hub';
    case 'Coding':   return 'terminal';
    case 'Vision':   return 'image_search';
    case 'Research': return 'manage_search';
    case 'Security': return 'shield';
    default:         return 'smart_toy';
  }
}

// ── Sub-components ─────────────────────────────────────────────────────────────

function Modal({ title, onClose, wide, children }: { title: string; onClose: () => void; wide?: boolean; children: React.ReactNode }) {
  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 100,
      background: 'rgba(0,0,0,0.65)', backdropFilter: 'blur(4px)',
      display: 'flex', alignItems: 'center', justifyContent: 'center',
    }} onClick={onClose}>
      <div style={{
        position: 'relative',
        background: 'linear-gradient(145deg, rgba(255,255,255,.028) 0%, rgba(255,255,255,.008) 36%, rgba(0,0,0,.045) 100%), rgba(10,22,38,.97)',
        border: '1px solid rgba(132,158,190,.2)',
        borderRadius: '18px',
        padding: '32px',
        width: wide ? '760px' : '580px',
        maxHeight: '90vh',
        overflowY: 'auto',
        boxShadow: '0 24px 64px rgba(0,0,0,.55), 0 6px 18px rgba(0,0,0,.3), inset 0 1px 0 rgba(255,255,255,.06), inset 0 -1px 0 rgba(0,0,0,.3)',
      }} onClick={(e) => e.stopPropagation()}>
        <div style={{
          position: 'absolute', inset: '1px', borderRadius: '17px', pointerEvents: 'none',
          boxShadow: 'inset 0 0 0 1px rgba(255,255,255,.018)',
        }} />
        <div style={{
          position: 'absolute', top: 0, left: '24px', right: '24px', height: '1px', pointerEvents: 'none',
          background: 'linear-gradient(90deg, transparent, rgba(40,215,238,.38), transparent)',
          borderRadius: '1px',
        }} />
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
          <h3 style={{ fontSize: '17px', fontWeight: 700, color: 'var(--tm-text)', letterSpacing: '-0.01em' }}>{title}</h3>
          <button onClick={onClose} style={{
            background: 'linear-gradient(145deg, rgba(255,255,255,.018), rgba(0,0,0,.05)), rgba(5,15,28,.5)',
            border: '1px solid rgba(132,157,188,.14)',
            boxShadow: 'inset 0 1px 0 rgba(255,255,255,.025), inset 0 -1px 0 rgba(0,0,0,.2)',
            borderRadius: '8px', cursor: 'pointer', color: 'var(--tm-text-muted)',
            fontSize: '16px', width: '32px', height: '32px', display: 'flex', alignItems: 'center', justifyContent: 'center',
          }}>✕</button>
        </div>
        {children}
      </div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: '16px' }}>
      <label style={{ display: 'block', fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', marginBottom: '6px', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
        {label}
      </label>
      {children}
    </div>
  );
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ fontSize: '10px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.07em', marginBottom: '8px' }}>
      {children}
    </div>
  );
}

function ChangedBadge({ old: oldVal, next: newVal }: { old: string; next: string }) {
  return (
    <span style={{ fontSize: '11px' }}>
      <span style={{ color: '#94a3b8', textDecoration: 'line-through', marginRight: '6px' }}>{oldVal || '—'}</span>
      <span style={{ color: '#4edea3', fontWeight: 600 }}>{newVal || '—'}</span>
    </span>
  );
}

function DiffRow({ label, changed, oldVal, newVal }: { label: string; changed: boolean; oldVal: string; newVal: string }) {
  if (!changed && !newVal) return null;
  return (
    <div style={{ display: 'flex', gap: '8px', alignItems: 'flex-start', padding: '6px 0', borderBottom: '1px solid var(--tm-border)' }}>
      <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)', minWidth: '110px', flexShrink: 0 }}>{label}</span>
      {changed
        ? <ChangedBadge old={oldVal} next={newVal} />
        : <span style={{ fontSize: '12px', color: 'var(--tm-text)' }}>{newVal}</span>
      }
    </div>
  );
}

const nestedSurface: React.CSSProperties = {
  background: 'linear-gradient(145deg, rgba(255,255,255,.018), rgba(0,0,0,.05)), rgba(5,15,28,.50)',
  border: '1px solid rgba(132,157,188,.12)',
  boxShadow: 'inset 0 1px 0 rgba(255,255,255,.025), inset 0 -1px 0 rgba(0,0,0,.2)',
};

const inputStyle: React.CSSProperties = {
  width: '100%', padding: '8px 12px', borderRadius: '8px',
  border: '1px solid rgba(132,157,188,.18)',
  background: 'linear-gradient(145deg, rgba(255,255,255,.018), rgba(0,0,0,.05)), rgba(5,15,28,.50)',
  boxShadow: 'inset 0 1px 0 rgba(255,255,255,.025), inset 0 -1px 0 rgba(0,0,0,.2)',
  fontSize: '14px', color: 'var(--tm-text)', boxSizing: 'border-box',
};

// Module-level set — survives tab switches (component unmount/remount)
const _inFlightScans = new Set<string>();

// ── Agent Card Component ───────────────────────────────────────────────────────

function AgentCard({
  agent,
  scanResult,
  testResult,
  isDiscovering,
  onTest,
  onScan,
  onDiscover,
  onEdit,
  onDelete,
  onOpenScanModal,
}: {
  agent: Agent;
  scanResult: ScanResult | 'scanning' | undefined;
  testResult: { ok: boolean; latency_ms: number; detail: string } | 'testing' | undefined;
  isDiscovering: boolean;
  onTest: () => void;
  onScan: () => void;
  onDiscover: () => void;
  onEdit: () => void;
  onDelete: () => void;
  onOpenScanModal: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const [showOverflow, setShowOverflow] = useState(false);
  const overflowRef = useRef<HTMLDivElement>(null);
  const isInternal = agent.tags?.includes('internal') || agent.slug === 'workflow_advisor';
  const category = agentCategory(agent);
  const accent = isInternal
    ? { color: '#a0f0d0', border: 'rgba(160,240,208,0.45)', glow: 'rgba(160,240,208,0.18)' }
    : categoryAccent(category);
  const catStyle = categoryBadgeStyle(category);
  const icon = agent.icon || agentIcon(agent, category);

  useEffect(() => {
    if (!showOverflow) return;
    function handler(e: MouseEvent) {
      if (overflowRef.current && !overflowRef.current.contains(e.target as Node)) {
        setShowOverflow(false);
      }
    }
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [showOverflow]);

  function copyEndpoint() {
    navigator.clipboard.writeText(agent.endpoint_url).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  }

  return (
    <article className="glass-card" style={{
      padding: '22px', display: 'flex', flexDirection: 'column', gap: '14px',
      borderRadius: '20px', position: 'relative',
      ...(isInternal ? { background: 'linear-gradient(160deg, rgba(0,160,120,0.08) 0%, rgba(0,80,60,0.06) 100%), rgba(10,18,32,0.92)' } : {}),
    }}>

      {/* ── Header row: icon + name/badges + overflow ── */}
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: '12px' }}>

        {/* Icon tile — polished gradient with category glow */}
        <div style={{
          width: '56px', height: '56px', flexShrink: 0, borderRadius: '14px',
          background: `radial-gradient(circle at 30% 25%, ${accent.glow}, transparent 65%),
                       linear-gradient(145deg, rgba(20,32,52,0.96), rgba(8,16,30,0.96))`,
          border: `1px solid ${accent.border}`,
          boxShadow: `0 0 18px ${accent.glow}, inset 0 1px 0 rgba(255,255,255,0.07)`,
          display: 'flex', alignItems: 'center', justifyContent: 'center',
        }}>
          <span className="material-symbols-outlined" style={{ fontSize: '26px', color: accent.color }}>{icon}</span>
        </div>

        {/* Name + status badges */}
        <div style={{ flex: 1, minWidth: 0, paddingTop: '2px' }}>
          <h3 style={{
            fontSize: '16px', fontWeight: 700, color: '#e2e8f0', margin: '0 0 6px 0',
            lineHeight: 1.2, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
          }}>{agent.display_name}</h3>

          <div style={{ display: 'flex', alignItems: 'center', gap: '6px', flexWrap: 'wrap' }}>
            {/* Enabled / disabled chip */}
            <span style={{
              fontSize: '9px', fontWeight: 700, letterSpacing: '0.07em', textTransform: 'uppercase',
              padding: '2px 7px', borderRadius: '9999px', display: 'inline-flex', alignItems: 'center', gap: '4px',
              background: agent.enabled ? 'rgba(16,185,129,0.12)' : 'rgba(100,116,139,0.12)',
              color: agent.enabled ? '#34d399' : '#64748b',
              border: `1px solid ${agent.enabled ? 'rgba(16,185,129,0.28)' : 'rgba(100,116,139,0.22)'}`,
            }}>
              {agent.enabled && <span style={{ width: '5px', height: '5px', borderRadius: '50%', background: '#34d399', boxShadow: '0 0 5px #34d399', display: 'inline-block' }} />}
              {agent.enabled ? 'Enabled' : 'Disabled'}
            </span>

            {/* Category badge */}
            <span style={{
              fontSize: '9px', fontWeight: 700, letterSpacing: '0.06em', textTransform: 'uppercase',
              padding: '2px 7px', borderRadius: '9999px', display: 'inline-block',
              ...catStyle,
            }}>{category}</span>

            {/* Internal badge — mini the-M logo */}
            {isInternal && (
              <span title="Internal the-M system component" style={{ display: 'inline-flex', alignItems: 'center', gap: 4, padding: '2px 7px', borderRadius: '9999px', background: 'rgba(160,240,208,0.1)', border: '1px solid rgba(160,240,208,0.25)' }}>
                <svg width="20" height="16" viewBox="0 0 1407 1118" style={{ opacity: 0.85, flexShrink: 0 }}>
                  <polygon points="88,77 184,146 244,191 281,217 336,259 355,272 358,272 367,267 372,266 379,262 391,258 433,239 440,237 473,222 513,206 520,202 546,192 555,187 558,187 446,102 433,91 421,83 403,68 397,65 392,60 331,15 318,4 274,19 264,21 246,28 239,29 217,37 214,37 211,39 201,41 189,46 186,46 154,57 151,57 148,59 141,60 138,62 104,73 101,73 98,75" fill="#a0f0d0"/>
                  <polygon points="1323,77 1313,75 1292,67 1289,67 1239,50 1236,50 1233,48 1230,48 1189,34 1176,31 1094,4 1085,12 1074,19 1053,36 959,106 855,187 876,196 881,197 973,237 980,239 1034,263 1048,268 1055,272 1059,272 1139,213 1146,209 1177,185 1188,178 1208,162 1284,107" fill="#a0f0d0"/>
                  <polygon points="70,97 70,334 71,335 72,350 76,365 104,429 108,435 180,486 184,490 245,534 345,609 339,293 305,269 281,250 252,230 182,177 179,176 153,156 150,155" fill="#a0f0d0"/>
                  <polygon points="1342,97 1252,162 1248,166 1152,236 1148,240 1126,255 1122,259 1112,265 1103,273 1074,293 1073,296 1073,317 1072,318 1072,355 1071,356 1071,415 1070,416 1070,461 1069,462 1069,526 1068,527 1067,609 1306,433 1325,392 1336,365 1341,343 1341,331 1342,330" fill="#a0f0d0"/>
                  <polygon points="682,361 576,210 381,292 532,410 577,395 580,395 586,392 595,390 613,384 616,382 622,381 664,367 667,365" fill="#a0f0d0"/>
                  <polygon points="732,361 803,384 806,386 809,386 831,394 834,394 860,404 863,404 881,410 1033,291 837,210 764,315 760,319 759,322 740,348" fill="#a0f0d0"/>
                  <polygon points="367,314 367,373 368,374 368,430 369,431 371,567 380,574 383,575 388,580 396,585 505,669 508,611 509,610 509,595 510,594 512,540 513,539 513,524 514,523 514,504 515,503 515,490 516,489 517,454 518,453 518,434 519,433 504,421 501,420 490,410 468,394 427,361 423,359 395,336 392,335" fill="#a0f0d0"/>
                  <polygon points="1046,314 894,433 895,456 896,457 896,475 897,476 897,494 898,495 898,513 899,514 901,561 902,562 902,579 903,580 903,594 904,595 906,650 907,651 907,666 908,669 934,648 971,621 1041,567" fill="#a0f0d0"/>
                  <polygon points="549,424 693,539 693,534 694,533 693,532 693,377 676,382 664,387 660,387 657,389 654,389 635,396 632,396" fill="#a0f0d0"/>
                  <polygon points="864,424 815,407 812,407 791,400 779,395 776,395 721,377 721,539 732,529 736,527 752,513" fill="#a0f0d0"/>
                  <polygon points="535,446 532,511 531,512 531,531 530,532 530,546 529,547 529,567 528,568 527,600 526,601 526,616 525,617 525,634 524,635 524,650 523,651 523,662 522,663 522,682 543,697 628,763 640,771 645,776 649,778 692,812 693,809 693,799 692,798 692,793 693,792 693,572 685,567 679,561 675,559 649,537 611,508 605,502 602,501" fill="#a0f0d0"/>
                  <polygon points="878,446 721,572 721,594 720,595 720,775 721,776 720,780 721,781 721,812 752,787 756,785 816,738 892,681 891,669 890,668 890,650 889,649 889,633 888,632 888,619 887,618 885,567 884,566 884,554 883,553 883,532 882,531 882,513 881,512" fill="#a0f0d0"/>
                  <polygon points="100,461 95,488 89,506 87,509 86,515 77,534 75,541 55,582 38,613 16,647 13,656 13,662 16,670 26,679 42,685 62,690 67,693 74,700 76,705 76,720 68,743 68,749 70,755 75,763 87,770 97,772 125,772 126,771 130,772 128,775 112,781 89,784 83,791 81,797 81,805 83,811 89,818 100,824 105,829 109,836 111,843 111,860 105,889 105,910 108,922 115,933 121,939 173,974 286,1057 326,1088 345,1105 345,641" fill="#a0f0d0"/>
                  <polygon points="1312,462 1273,489 1230,522 1227,523 1143,586 1067,641 1067,1106 1080,1093 1135,1050 1138,1049 1172,1023 1235,978 1239,974 1249,968 1253,964 1256,963 1270,952 1292,938 1301,928 1305,920 1307,912 1308,897 1307,896 1307,888 1301,858 1302,839 1307,829 1312,824 1323,818 1328,813 1331,806 1331,796 1330,792 1324,784 1311,783 1297,780 1286,776 1282,773 1284,771 1287,772 1316,772 1328,769 1335,765 1340,760 1344,750 1344,742 1336,717 1336,706 1339,699 1344,694 1353,689 1371,685 1386,679 1394,673 1399,663 1399,655 1397,649 1372,610 1339,546 1321,500 1321,497 1316,484" fill="#a0f0d0"/>
                </svg>
                <span style={{ fontSize: '9px', fontWeight: 700, color: '#a0f0d0', letterSpacing: '0.06em', textTransform: 'uppercase' }}>internal</span>
              </span>
            )}
          </div>

          {/* Security score badge — shown below name when scan result exists */}
          {scanResult && scanResult !== 'scanning' && (() => {
            const rc = riskColors(scanResult.risk);
            return (
              <button onClick={onOpenScanModal} title="View security report" style={{
                marginTop: '5px', display: 'inline-flex', alignItems: 'center', gap: '4px',
                fontSize: '9px', fontWeight: 700, letterSpacing: '0.05em', textTransform: 'uppercase',
                padding: '2px 8px', borderRadius: '9999px', cursor: 'pointer',
                background: rc.bg, border: `1px solid ${rc.border}`, color: rc.color,
                boxShadow: `0 0 8px ${rc.glow}`,
              }}>
                <span style={{ fontSize: '10px' }}>🛡</span>{scanResult.score} · {scanResult.risk} risk
              </button>
            );
          })()}
          {scanResult === 'scanning' && (
            <span style={{
              marginTop: '5px', display: 'inline-flex', alignItems: 'center', gap: '4px',
              fontSize: '9px', fontWeight: 700, letterSpacing: '0.05em', textTransform: 'uppercase',
              padding: '2px 8px', borderRadius: '9999px',
              background: 'rgba(167,139,250,0.12)', border: '1px solid rgba(167,139,250,0.28)', color: '#a78bfa',
              animation: 'pulse 1.6s ease-in-out infinite',
            }}>
              <span style={{ fontSize: '10px' }}>🛡</span> Scanning…
            </span>
          )}
        </div>

        {/* Three-dot overflow menu */}
        <div ref={overflowRef} style={{ position: 'relative', flexShrink: 0 }}>
          <button onClick={() => setShowOverflow(v => !v)} style={{
            width: '32px', height: '32px', borderRadius: '8px', cursor: 'pointer',
            background: 'rgba(30,41,59,0.5)', border: '1px solid rgba(255,255,255,0.06)',
            color: '#64748b', display: 'flex', alignItems: 'center', justifyContent: 'center',
            transition: 'color 150ms ease, border-color 150ms ease',
          }}>
            <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor">
              <circle cx="8" cy="3" r="1.5"/><circle cx="8" cy="8" r="1.5"/><circle cx="8" cy="13" r="1.5"/>
            </svg>
          </button>
          {showOverflow && (
            <div style={{
              position: 'absolute', top: '36px', right: 0, zIndex: 20,
              background: '#0c1830', border: '1px solid rgba(255,255,255,0.08)',
              borderRadius: '10px', overflow: 'hidden', minWidth: '130px',
              boxShadow: '0 8px 28px rgba(0,0,0,0.5)',
            }} onClick={() => setShowOverflow(false)}>
              <button onClick={onEdit} style={{ width: '100%', padding: '9px 14px', textAlign: 'left', background: 'none', border: 'none', color: '#cbd5e1', fontSize: '12px', cursor: 'pointer' }}>
                ✎ Edit
              </button>
              <div style={{ height: '1px', background: 'rgba(255,255,255,0.05)', margin: '0 8px' }} />
              <button onClick={onDelete} style={{ width: '100%', padding: '9px 14px', textAlign: 'left', background: 'none', border: 'none', color: '#f87171', fontSize: '12px', cursor: 'pointer' }}>
                ✕ Delete
              </button>
            </div>
          )}
        </div>
      </div>

      {/* ── Description — 2 lines max ── */}
      <p style={{
        fontSize: '13px', color: '#94a3b8', lineHeight: 1.55, margin: 0,
        display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden',
        minHeight: '40px',
      }}>
        {agent.description || <span style={{ opacity: 0.35 }}>No description</span>}
      </p>

      {/* ── Stats: two compact equal tiles ── */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '8px' }}>
        {/* Skills tile */}
        <div style={{
          padding: '10px 12px', borderRadius: '10px',
          background: 'rgba(0,0,0,0.28)', border: '1px solid rgba(255,255,255,0.05)',
          display: 'flex', alignItems: 'center', gap: '8px',
        }}>
          <span className="material-symbols-outlined" style={{ fontSize: '16px', color: '#94a3b8', flexShrink: 0 }}>hub</span>
          <div>
            <p style={{ fontSize: '15px', fontWeight: 700, color: '#e2e8f0', margin: 0, lineHeight: 1 }}>
              {agent.skills && agent.skills.length > 0 ? agent.skills.length : '—'}
            </p>
            <p style={{ fontSize: '9px', color: '#94a3b8', textTransform: 'uppercase', fontWeight: 700, letterSpacing: '0.08em', margin: '2px 0 0 0' }}>skills</p>
          </div>
        </div>
        {/* Last sync tile */}
        <div style={{
          padding: '10px 12px', borderRadius: '10px',
          background: 'rgba(0,0,0,0.28)', border: '1px solid rgba(255,255,255,0.05)',
          display: 'flex', alignItems: 'center', gap: '8px',
        }}>
          <span className="material-symbols-outlined" style={{ fontSize: '16px', color: '#94a3b8', flexShrink: 0 }}>sync</span>
          <div>
            <p style={{ fontSize: '13px', fontWeight: 700, color: '#e2e8f0', margin: 0, lineHeight: 1, whiteSpace: 'nowrap' }}>
              {agent.card_fetched_at ? timeAgo(agent.card_fetched_at) : '—'}
            </p>
            <p style={{ fontSize: '9px', color: '#94a3b8', textTransform: 'uppercase', fontWeight: 700, letterSpacing: '0.08em', margin: '2px 0 0 0' }}>last sync</p>
          </div>
        </div>
      </div>

      {/* ── Endpoint field ── */}
      <div>
        <p style={{ fontSize: '9px', fontWeight: 700, color: '#94a3b8', textTransform: 'uppercase', letterSpacing: '0.08em', margin: '0 0 5px 0' }}>Endpoint</p>
        <div style={{
          display: 'flex', alignItems: 'center',
          background: 'rgba(0,0,0,0.35)', border: '1px solid rgba(255,255,255,0.05)',
          borderRadius: '8px', padding: '7px 10px', gap: '6px',
        }}>
          <span style={{
            fontSize: '11px', color: '#cbd5e1', fontFamily: 'monospace',
            overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: 1,
          }}>
            {agent.endpoint_url || '—'}
          </span>
          <button onClick={copyEndpoint} title="Copy" style={{
            background: 'none', border: 'none', cursor: 'pointer', flexShrink: 0, padding: '2px 4px',
            color: copied ? '#34d399' : '#94a3b8', fontSize: '14px',
            transition: 'color 150ms ease',
          }}>
            {copied
              ? <span className="material-symbols-outlined" style={{ fontSize: '14px' }}>check</span>
              : <span className="material-symbols-outlined" style={{ fontSize: '14px' }}>content_copy</span>
            }
          </button>
        </div>
      </div>

      {/* ── Test result inline ── */}
      {testResult && testResult !== 'testing' && (() => {
        const r = testResult as { ok: boolean; latency_ms: number; detail: string };
        return (
          <div style={{
            fontSize: '11px', padding: '6px 10px', borderRadius: '6px',
            background: r.ok ? 'rgba(16,185,129,0.08)' : 'rgba(220,38,38,0.08)',
            border: `1px solid ${r.ok ? 'rgba(16,185,129,0.2)' : 'rgba(220,38,38,0.2)'}`,
            color: r.ok ? '#34d399' : '#f87171',
          }}>
            {r.ok ? `✓ ${r.latency_ms}ms — ` : '✗ '}{r.detail}
          </div>
        );
      })()}

      {/* ── Action buttons: Test / Discover / Scan — equal width ── */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: '8px', marginTop: 'auto', paddingTop: '4px' }}>
        {/* Test — primary cyan */}
        <button
          onClick={onTest}
          disabled={testResult === 'testing'}
          className="card-action-btn card-action-btn--primary"
        >
          <span className="material-symbols-outlined" style={{ fontSize: '14px' }}>play_arrow</span>
          {testResult === 'testing' ? 'Testing…' : 'Test'}
        </button>

        {/* Discover — secondary dark */}
        <button
          onClick={onDiscover}
          disabled={isDiscovering}
          className="card-action-btn card-action-btn--secondary"
        >
          <span className="material-symbols-outlined" style={{ fontSize: '14px' }}>radar</span>
          {isDiscovering ? 'Loading…' : 'Discover'}
        </button>

        {/* Security Scan — secondary dark with shield tint */}
        <button
          onClick={onScan}
          disabled={scanResult === 'scanning'}
          className="card-action-btn card-action-btn--scan"
          title="Security scan"
        >
          <span className="material-symbols-outlined" style={{ fontSize: '14px' }}>security</span>
          {scanResult === 'scanning' ? 'Scanning…' : 'Scan'}
        </button>
      </div>
    </article>
  );
}

// ── Deploy CTA card ────────────────────────────────────────────────────────────

function DeployCard({ onClick }: { onClick: () => void }) {
  return (
    <article
      onClick={onClick}
      style={{
        borderRadius: '24px',
        border: '1px dashed rgba(99,102,241,0.5)',
        background: 'rgba(15,23,42,0.2)',
        display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
        gap: '16px', padding: '48px 24px', cursor: 'pointer', minHeight: '280px',
        transition: 'border-color 200ms ease, background 200ms ease',
      }}
      className="deploy-card"
    >
      <div style={{
        width: '56px', height: '56px', borderRadius: '16px',
        background: 'rgba(99,102,241,0.1)', border: '1px dashed rgba(99,102,241,0.4)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        fontSize: '28px', color: '#6366f1',
      }}>+</div>
      <div style={{ textAlign: 'center' }}>
        <p style={{ fontSize: '16px', fontWeight: 600, color: '#e2e8f0', margin: '0 0 6px 0' }}>Deploy a new agent</p>
        <p style={{ fontSize: '13px', color: '#94a3b8', margin: 0 }}>Connect an A2A agent endpoint</p>
      </div>
      <button style={{
        padding: '10px 24px', borderRadius: '8px', border: 'none', cursor: 'pointer',
        background: '#6366f1', color: '#fff', fontSize: '13px', fontWeight: 600,
        boxShadow: '0 0 15px rgba(99,102,241,0.3)',
      }}>
        Deploy New Agent
      </button>
    </article>
  );
}

// ── Main page ─────────────────────────────────────────────────────────────────

export default function AdminAgentsPage() {
  const [agents, setAgents] = useState<Agent[]>([]);
  const [orchestrators, setOrchestrators] = useState<OrchestratorFull[]>([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);
  const [editing, setEditing] = useState<Agent | null>(null);
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [deleteTarget, setDeleteTarget] = useState<Agent | null>(null);
  const [testResults, setTestResults] = useState<Record<string, { ok: boolean; latency_ms: number; detail: string } | 'testing'>>({});
  const [rowDiscoverState, setRowDiscoverState] = useState<Record<string, 'discovering'>>({});
  const [discoverPopup, setDiscoverPopup] = useState<{ agent: Agent; result: DiscoverResult; diff: CardDiff } | null>(null);
  const [applyingDiscover, setApplyingDiscover] = useState(false);
  const [discovering, setDiscovering] = useState(false);
  const [discoverError, setDiscoverError] = useState('');
  const [scanResults, setScanResults] = useState<Record<string, ScanResult | 'scanning'>>({});
  const [scanModal, setScanModal] = useState<{ agent: Agent; result: ScanResult } | null>(null);
  const dashWsRef = useRef<WebSocket | null>(null);
  const agentIdsKeyRef = useRef<string>('');
  const [searchTerm, setSearchTerm] = useState('');
  const [activeCategory, setActiveCategory] = useState('All');

  const reload = () => {
    Promise.all([themApi.agents(), themApi.orchestrators()])
      .then(([a, o]) => {
        setAgents(a);
        setOrchestrators(o);
        setScanResults((prev) => {
          const next = { ...prev };
          for (const agent of a) {
            if (_inFlightScans.has(agent.id)) {
              if (agent.last_scan_result) {
                next[agent.id] = agent.last_scan_result;
                _inFlightScans.delete(agent.id);
              } else {
                next[agent.id] = 'scanning';
              }
            } else if (agent.last_scan_result && !next[agent.id]) {
              next[agent.id] = agent.last_scan_result;
            } else if (agent.last_scan_result && next[agent.id] === 'scanning') {
              next[agent.id] = agent.last_scan_result;
            }
          }
          return next;
        });
      })
      .finally(() => setLoading(false));
  };

  useEffect(() => { reload(); }, []);

  // Dashboard WS — subscribe to agent:<id> channels for live scan events
  useEffect(() => {
    if (agents.length === 0) return;
    const key = agents.map((a) => a.id).sort().join(',');
    if (key === agentIdsKeyRef.current) return;
    agentIdsKeyRef.current = key;

    if (dashWsRef.current) {
      dashWsRef.current.close();
      dashWsRef.current = null;
    }

    let ws: WebSocket;
    fetch('/api/auth/token')
      .then((r) => r.json())
      .then((data: { access_token?: string }) => {
        if (!data.access_token) return;
        const wsBase = window.location.origin
          .replace('http://', 'ws://')
          .replace('https://', 'wss://');
        ws = new WebSocket(`${wsBase}/ws/dashboard?token=${data.access_token}`);
        dashWsRef.current = ws;

        ws.onopen = () => {
          ws.send(JSON.stringify({
            type: 'subscribe',
            channels: agents.map((a) => `agent:${a.id}`),
          }));
        };

        ws.onmessage = (e) => {
          try {
            const msg = JSON.parse(e.data);
            if (msg.type === 'ping') return;
            const ch: string = msg.channel ?? '';
            if (!ch.startsWith('agent:')) return;
            const event = msg.event ?? {};
            const agentId: string = event.agent_id ?? '';
            if (event.type === 'scan_started') {
              _inFlightScans.add(agentId);
              setScanResults((r) => ({ ...r, [agentId]: 'scanning' }));
            } else if (event.type === 'scan_complete') {
              _inFlightScans.delete(agentId);
              const partial: ScanResult = {
                score: event.score,
                risk: event.risk,
                summary: event.summary,
                findings: event.findings ?? [],
                http_probes: event.http_probes ?? { tls: '', auth_required: '', reachable: false },
                scanned_at: event.scanned_at ?? new Date().toISOString(),
              };
              setScanResults((r) => ({ ...r, [agentId]: partial }));
              reload();
            } else if (event.type === 'scan_failed') {
              _inFlightScans.delete(agentId);
              setScanResults((r) => {
                const n = { ...r };
                delete n[agentId];
                return n;
              });
              alert(`Scan failed: ${event.error ?? 'unknown error'}`);
            }
          } catch { /* ignore parse errors */ }
        };

        ws.onerror = () => { /* silent */ };
      })
      .catch(() => { /* auth fetch failed */ });

    return () => {
      if (dashWsRef.current) {
        dashWsRef.current.close();
        dashWsRef.current = null;
      }
      agentIdsKeyRef.current = '';
    };
  }, [agents]);

  function openCreate() {
    setEditing(null);
    setForm(EMPTY_FORM);
    setError('');
    setDiscoverError('');
    setShowModal(true);
  }

  function openEdit(agent: Agent) {
    setEditing(agent);
    setForm({
      slug: agent.slug,
      display_name: agent.display_name,
      description: agent.description || '',
      transport: agent.transport,
      endpoint_url: agent.endpoint_url,
      auth_token: '',
      max_concurrency: agent.max_concurrency,
      timeout_seconds: agent.timeout_seconds,
      enabled: agent.enabled,
      skills: agent.skills || [],
      supports_streaming: agent.supports_streaming || false,
      supports_push: agent.supports_push || false,
      icon: agent.icon || '',
      agent_card: agent.agent_card || null,
      agent_card_url: agent.agent_card_url || '',
    });
    setError('');
    setDiscoverError('');
    setShowModal(true);
  }

  async function handleDiscover() {
    if (!form.endpoint_url.trim()) { setDiscoverError('Enter an endpoint URL first'); return; }
    setDiscovering(true);
    setDiscoverError('');
    try {
      const result = await themApi.discoverAgent({ endpoint_url: form.endpoint_url.trim(), auth_token: form.auth_token || undefined });
      if (!result.ok) { setDiscoverError(result.detail || 'Discovery failed'); return; }
      setForm((f) => ({
        ...f,
        display_name: result.display_name || f.display_name,
        slug: editing ? f.slug : (result.suggested_slug || f.slug),
        description: result.description || f.description,
        skills: result.skills,
        supports_streaming: result.supports_streaming,
        supports_push: result.supports_push,
        ...(result.icon ? { icon: result.icon } : {}),
        agent_card: result.agent_card,
        agent_card_url: result.agent_card_url,
      }));
    } catch (e: unknown) {
      setDiscoverError(e instanceof Error ? e.message : 'Discovery failed');
    } finally {
      setDiscovering(false);
    }
  }

  async function handleSave() {
    setSaving(true);
    setError('');
    try {
      const body: Record<string, unknown> = { ...form };
      if (!body.auth_token) delete body.auth_token;
      if (!body.icon) body.icon = null;
      if (editing) {
        delete body.slug;
        await themApi.updateAgent(editing.id, body);
      } else {
        await themApi.createAgent(body);
      }
      setShowModal(false);
      reload();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  }

  async function handleTest(agent: Agent) {
    setTestResults((r) => ({ ...r, [agent.id]: 'testing' }));
    try {
      const result = await themApi.testAgent(agent.id);
      setTestResults((r) => ({ ...r, [agent.id]: result }));
    } catch (e: unknown) {
      setTestResults((r) => ({ ...r, [agent.id]: { ok: false, latency_ms: 0, detail: e instanceof Error ? e.message : 'Test failed' } }));
    }
  }

  async function handleScan(agent: Agent) {
    _inFlightScans.add(agent.id);
    setScanResults((r) => ({ ...r, [agent.id]: 'scanning' }));
    setScanModal(null);
    try {
      await themApi.scanAgent(agent.id);
    } catch (e: unknown) {
      _inFlightScans.delete(agent.id);
      setScanResults((r) => { const n = { ...r }; delete n[agent.id]; return n; });
      alert(e instanceof Error ? e.message : 'Scan failed');
    }
  }

  async function handleRowDiscover(agent: Agent) {
    setRowDiscoverState((r) => ({ ...r, [agent.id]: 'discovering' }));
    try {
      const result = await themApi.discoverAgent({ endpoint_url: agent.endpoint_url, agent_id: agent.id });
      if (!result.ok) { alert(`Discovery failed: ${result.detail}`); return; }
      const diff = buildDiff(agent, result);
      setDiscoverPopup({ agent, result, diff });
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : 'Discovery failed');
    } finally {
      setRowDiscoverState((r) => { const n = { ...r }; delete n[agent.id]; return n; });
    }
  }

  async function handleApplyDiscover() {
    if (!discoverPopup) return;
    const { agent, result, diff } = discoverPopup;

    const affected = orchestrators.filter(o => o.allowed_agent_ids.includes(agent.id));
    if (affected.length > 0 && diff.hasChanges) {
      const names = affected.map(o => o.display_name || o.name).join(', ');
      const confirmed = window.confirm(
        `This agent is used by ${affected.length} orchestrator${affected.length > 1 ? 's' : ''}: ${names}\n\nTheir tool descriptions will update on the next run. Continue?`
      );
      if (!confirmed) return;
    }

    setApplyingDiscover(true);
    try {
      await themApi.updateAgent(agent.id, {
        display_name: result.display_name || agent.display_name,
        description: result.description || agent.description,
        skills: result.skills,
        supports_streaming: result.supports_streaming,
        supports_push: result.supports_push,
        ...(result.icon ? { icon: result.icon } : {}),
        agent_card: result.agent_card,
        agent_card_url: result.agent_card_url,
      });
      setDiscoverPopup(null);
      reload();
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : 'Apply failed');
    } finally {
      setApplyingDiscover(false);
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    try {
      await themApi.deleteAgent(deleteTarget.id);
      setDeleteTarget(null);
      reload();
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : 'Delete failed');
    }
  }

  const set = (k: keyof FormState, v: unknown) => setForm((f) => ({ ...f, [k]: v }));

  const CATEGORY_PILLS = ['All', 'Enabled', 'A2A', 'Vision', 'Coding', 'Research'];

  const filteredAgents = agents.filter((a) => {
    const q = searchTerm.toLowerCase();
    const matchSearch = !q ||
      a.display_name.toLowerCase().includes(q) ||
      a.slug.toLowerCase().includes(q) ||
      (a.description ?? '').toLowerCase().includes(q) ||
      (a.endpoint_url ?? '').toLowerCase().includes(q);
    const cat = agentCategory(a);
    const matchCategory =
      activeCategory === 'All' ? true :
      activeCategory === 'Enabled' ? a.enabled :
      cat === activeCategory;
    return matchSearch && matchCategory;
  });

  return (
    <AuthGuard>
      <style>{`
/* ── Glass card ─────────────────────────────────────── */
        .glass-card {
          background:
            linear-gradient(160deg, rgba(255,255,255,0.032) 0%, rgba(255,255,255,0.006) 40%, rgba(0,0,0,0.06) 100%),
            rgba(10,18,32,0.92);
          border: 1px solid rgba(255,255,255,0.07);
          backdrop-filter: blur(12px);
          box-shadow:
            0 8px 32px rgba(0,0,0,0.4),
            0 2px 8px rgba(0,0,0,0.25),
            inset 0 1px 0 rgba(255,255,255,0.04);
          transition: border-color 240ms ease, box-shadow 240ms ease;
        }
        .glass-card:hover {
          border-color: rgba(0,209,255,0.28);
          box-shadow:
            0 8px 32px rgba(0,0,0,0.5),
            0 2px 8px rgba(0,0,0,0.28),
            0 0 0 1px rgba(0,209,255,0.1),
            0 0 32px rgba(0,209,255,0.08),
            inset 0 1px 0 rgba(255,255,255,0.055);
        }
        .glass-card:active {
          box-shadow:
            0 4px 16px rgba(0,0,0,0.5),
            inset 0 1px 0 rgba(255,255,255,0.03);
          border-color: rgba(0,209,255,0.4);
          transition: border-color 80ms ease, box-shadow 80ms ease;
        }

        /* ── Deploy card hover ─────────────────────────────── */
        .deploy-card:hover {
          border-color: rgba(99,102,241,0.7) !important;
          background: rgba(99,102,241,0.04) !important;
        }

        /* ── Card action buttons — equal-width three-column ── */
        .card-action-btn {
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
        .card-action-btn:disabled { opacity: 0.45; cursor: not-allowed; }

        /* Primary — solid cyan */
        .card-action-btn--primary {
          background: #00d1ff;
          color: #021520;
          border: none;
          box-shadow: 0 0 14px rgba(0,209,255,0.38);
        }
        .card-action-btn--primary:hover:not(:disabled) {
          background: #22dcff;
          box-shadow: 0 0 22px rgba(0,209,255,0.55);
        }
        .card-action-btn--primary:active:not(:disabled) {
          background: #00b8e0;
          box-shadow: 0 0 10px rgba(0,209,255,0.3);
        }

        /* Secondary — dark ghost (Discover) */
        .card-action-btn--secondary {
          background: rgba(30,41,59,0.55);
          color: #94a3b8;
          border: 1px solid rgba(255,255,255,0.08);
        }
        .card-action-btn--secondary:hover:not(:disabled) {
          border-color: rgba(129,140,248,0.45);
          color: #818cf8;
          background: rgba(99,102,241,0.1);
        }

        /* Scan — dark with cyan-shield tint */
        .card-action-btn--scan {
          background: rgba(30,41,59,0.55);
          color: #94a3b8;
          border: 1px solid rgba(255,255,255,0.08);
        }
        .card-action-btn--scan:hover:not(:disabled) {
          border-color: rgba(0,209,255,0.42);
          color: #00d1ff;
          background: rgba(0,209,255,0.08);
        }

        /* Legacy button classes (used in modals) */
        @keyframes pulse-border {
          0%, 100% { box-shadow: 0 0 0 0 rgba(124,58,237,0.5); }
          50% { box-shadow: 0 0 0 6px rgba(124,58,237,0); }
        }
        .save-pulse { animation: pulse-border 1.4s ease-in-out infinite; }

        @keyframes pulse {
          0%, 100% { opacity: 1; }
          50% { opacity: 0.45; }
        }

        /* ── Filter pills ─────────────────────────────────────── */
        .filter-pill {
          padding: 6px 16px; border-radius: 9999px;
          border: 1px solid rgba(255,255,255,0.08);
          background: rgba(255,255,255,0.04);
          color: #64748b; font-size: 12px; font-weight: 600;
          cursor: pointer; white-space: nowrap;
          transition: border-color 150ms ease, color 150ms ease, background 150ms ease;
        }
        .filter-pill:hover { border-color: rgba(255,255,255,0.14); color: #94a3b8; background: rgba(255,255,255,0.07); }
        .filter-pill-active {
          padding: 6px 16px; border-radius: 9999px;
          border: 1px solid rgba(0,209,255,0.45);
          background: rgba(0,209,255,0.12);
          color: #00d1ff; font-size: 12px; font-weight: 700;
          cursor: pointer; white-space: nowrap;
          transition: border-color 150ms ease;
        }

        .ghost-btn {
          padding: 5px 11px;
          border-radius: 7px;
          border: 1px solid rgba(132,158,190,.18);
          background: linear-gradient(145deg, rgba(255,255,255,.018), rgba(0,0,0,.05)), rgba(5,15,28,.40);
          box-shadow: inset 0 1px 0 rgba(255,255,255,.025), inset 0 -1px 0 rgba(0,0,0,.18);
          cursor: pointer;
          font-size: 12px;
          color: var(--tm-text-muted);
          transition: border-color 160ms ease, color 160ms ease;
        }
        .ghost-btn:hover { border-color: rgba(132,158,190,.32); color: var(--tm-text); }

        .delete-btn {
          padding: 5px 11px;
          border-radius: 7px;
          border: 1px solid rgba(220,38,38,.22);
          background: linear-gradient(145deg, rgba(220,38,38,.06) 0%, rgba(185,28,28,.02) 100%), rgba(5,15,28,.40);
          box-shadow: inset 0 1px 0 rgba(255,255,255,.025), inset 0 -1px 0 rgba(0,0,0,.18);
          cursor: pointer;
          font-size: 12px;
          color: #f87171;
          transition: border-color 160ms ease, box-shadow 160ms ease;
        }
        .delete-btn:hover {
          border-color: rgba(220,38,38,.42);
          box-shadow: 0 3px 10px rgba(220,38,38,.1), inset 0 1px 0 rgba(255,255,255,.03), inset 0 -1px 0 rgba(0,0,0,.2);
        }

        .discover-btn {
          padding: 5px 11px;
          border-radius: 7px;
          border: 1px solid rgba(167,139,250,.28);
          background: linear-gradient(145deg, rgba(167,139,250,.08) 0%, rgba(124,58,237,.04) 100%), rgba(5,15,28,.50);
          box-shadow: inset 0 1px 0 rgba(255,255,255,.05), inset 0 -1px 0 rgba(0,0,0,.2);
          cursor: pointer;
          font-size: 12px;
          font-weight: 600;
          color: #a78bfa;
          transition: border-color 160ms ease, box-shadow 160ms ease, transform 160ms ease;
        }
        .discover-btn:hover:not(:disabled) {
          border-color: rgba(167,139,250,.45);
          box-shadow: 0 4px 12px rgba(167,139,250,.1), inset 0 1px 0 rgba(255,255,255,.07), inset 0 -1px 0 rgba(0,0,0,.25);
          transform: translateY(-1px);
        }
        .discover-btn:disabled { opacity: 0.5; cursor: not-allowed; }

        .score-badge {
          display: inline-flex;
          align-items: center;
          gap: 5px;
          padding: 3px 9px;
          border-radius: 5px;
          font-size: 10px;
          font-weight: 700;
          letter-spacing: 0.04em;
          cursor: pointer;
          border: 1px solid;
          transition: filter 160ms ease, transform 160ms ease;
          box-shadow: inset 0 1px 0 rgba(255,255,255,.04), inset 0 -1px 0 rgba(0,0,0,.18);
        }
        .score-badge:hover { filter: brightness(1.15); transform: translateY(-1px); }

        .scanning-pill {
          display: inline-flex;
          align-items: center;
          gap: 5px;
          padding: 3px 9px;
          border-radius: 5px;
          font-size: 10px;
          font-weight: 700;
          letter-spacing: 0.04em;
          background: linear-gradient(145deg, rgba(167,139,250,.14), rgba(124,58,237,.06));
          border: 1px solid rgba(167,139,250,.28);
          color: #c4b5fd;
          box-shadow: inset 0 1px 0 rgba(255,255,255,.04);
          animation: pulse 1.6s ease-in-out infinite;
        }

        .score-ring {
          position: relative;
          width: 80px;
          height: 80px;
          border-radius: 50%;
          display: flex;
          flex-direction: column;
          align-items: center;
          justify-content: center;
          flex-shrink: 0;
          box-shadow: 0 6px 20px rgba(0,0,0,.3), inset 0 1px 0 rgba(255,255,255,.06), inset 0 -1px 0 rgba(0,0,0,.25);
        }

        .probe-row {
          display: flex;
          align-items: center;
          justify-content: space-between;
          padding: 7px 12px;
          border-radius: 8px;
          margin-bottom: 6px;
        }

        .finding-card {
          padding: 11px 13px;
          border-radius: 10px;
          margin-bottom: 7px;
          transition: border-color 160ms ease;
        }
        .finding-card:hover { border-color: rgba(132,158,190,.22) !important; }

        .btn-primary-scan {
          padding: 9px 22px;
          border-radius: 9px;
          border: 1px solid rgba(40,215,238,.48);
          background: linear-gradient(180deg, rgba(40,215,238,.22) 0%, rgba(24,197,223,.12) 100%), rgba(5,15,28,.80);
          box-shadow: 0 6px 18px rgba(40,215,238,.12), inset 0 1px 0 rgba(255,255,255,.10), inset 0 -1px 0 rgba(0,0,0,.24);
          color: #28d7ee;
          font-size: 14px;
          font-weight: 700;
          cursor: pointer;
          letter-spacing: 0.01em;
          transition: border-color 160ms ease, box-shadow 160ms ease, transform 160ms ease;
          display: flex;
          align-items: center;
          gap: 7px;
        }
        .btn-primary-scan:hover:not(:disabled) {
          border-color: rgba(40,215,238,.70);
          box-shadow: 0 8px 24px rgba(40,215,238,.18), inset 0 1px 0 rgba(255,255,255,.14), inset 0 -1px 0 rgba(0,0,0,.28);
          transform: translateY(-1px);
        }
        .btn-primary-scan:disabled { opacity: 0.5; cursor: not-allowed; }

        @media (prefers-reduced-motion: reduce) {
          .glass-card { transition: none !important; }
          .scanning-pill { animation: none !important; }
        }
      `}</style>

      <div style={{ display: 'flex', minHeight: '100vh', background: '#060a14' }}>
        <Sidebar />
        <main style={{ marginLeft: '260px', flex: 1, background: '#060a14' }}>

          {/* Page header */}
          <div style={{
            padding: '40px 32px 24px',
            display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          }}>
            <div>
              <h2 style={{ fontSize: '40px', fontWeight: 800, color: '#fff', margin: '0 0 6px 0', letterSpacing: '-0.03em', lineHeight: 1.1 }}>Agents</h2>
              <p style={{ fontSize: '14px', color: '#94a3b8', margin: 0 }}>Manage A2A (Agent-to-Agent) orchestrators and node connectors.</p>
            </div>
            <button onClick={openCreate} style={{
              display: 'flex', alignItems: 'center', gap: '8px',
              padding: '12px 24px', borderRadius: '8px', border: 'none', cursor: 'pointer',
              background: '#00d1ff', color: '#000', fontSize: '14px', fontWeight: 700,
              boxShadow: '0 0 20px rgba(0,209,255,0.4)',
              transition: 'box-shadow 200ms ease, transform 200ms ease',
            }}>
              <span style={{ fontSize: '18px', lineHeight: 1 }}>+</span>
              Deploy New Agent
            </button>
          </div>

          {/* Filter bar */}
          <div style={{ padding: '0 32px 28px' }}>
            <div style={{
              display: 'flex', alignItems: 'center', gap: '12px',
              background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.06)',
              borderRadius: '12px', padding: '10px 16px',
            }}>
              {/* Search */}
              <div style={{ position: 'relative', flex: '0 0 240px' }}>
                <span style={{ position: 'absolute', left: '10px', top: '50%', transform: 'translateY(-50%)', color: '#94a3b8', fontSize: '14px', pointerEvents: 'none' }}>🔍</span>
                <input
                  type="text"
                  placeholder="Search agents…"
                  value={searchTerm}
                  onChange={(e) => setSearchTerm(e.target.value)}
                  style={{
                    width: '100%', padding: '7px 12px 7px 32px', borderRadius: '8px', boxSizing: 'border-box',
                    background: 'rgba(0,0,0,0.3)', border: '1px solid rgba(255,255,255,0.07)',
                    color: '#e2e8f0', fontSize: '13px', outline: 'none',
                  }}
                />
              </div>
              {/* Divider */}
              <div style={{ width: '1px', height: '20px', background: 'rgba(255,255,255,0.08)', flexShrink: 0 }} />
              {/* Category pills */}
              {CATEGORY_PILLS.map((cat) => (
                <button
                  key={cat}
                  onClick={() => setActiveCategory(cat)}
                  className={activeCategory === cat ? 'filter-pill-active' : 'filter-pill'}
                >
                  {cat}
                </button>
              ))}
            </div>
          </div>

          {/* Card grid */}
          <div style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(3, 1fr)',
            gap: '24px',
            padding: '0 32px 48px',
          }}>
            {loading && (
              <div style={{ gridColumn: '1 / -1', padding: '80px', textAlign: 'center', color: '#94a3b8', fontSize: '14px' }}>
                Loading agents…
              </div>
            )}

            {!loading && agents.length === 0 && (
              <DeployCard onClick={openCreate} />
            )}

            {!loading && agents.length > 0 && filteredAgents.length === 0 && (
              <div style={{ gridColumn: '1 / -1', padding: '60px', textAlign: 'center', color: '#94a3b8', fontSize: '14px' }}>
                No agents match your filter
              </div>
            )}

            {!loading && filteredAgents.map((agent) => (
              <AgentCard
                key={agent.id}
                agent={agent}
                scanResult={scanResults[agent.id]}
                testResult={testResults[agent.id]}
                isDiscovering={!!rowDiscoverState[agent.id]}
                onTest={() => handleTest(agent)}
                onScan={() => handleScan(agent)}
                onDiscover={() => handleRowDiscover(agent)}
                onEdit={() => openEdit(agent)}
                onDelete={() => setDeleteTarget(agent)}
                onOpenScanModal={() => {
                  const sr = scanResults[agent.id];
                  if (sr && sr !== 'scanning') setScanModal({ agent, result: sr });
                }}
              />
            ))}

            {!loading && agents.length > 0 && (
              <DeployCard onClick={openCreate} />
            )}
          </div>
        </main>

        {/* ── Create / Edit Modal ── */}
        {showModal && (
          <Modal title={editing ? `Edit — ${editing.display_name}` : 'New Agent'} onClose={() => setShowModal(false)}>
            <Field label="Endpoint URL">
              <div style={{ display: 'flex', gap: '8px' }}>
                <input style={{ ...inputStyle, flex: 1 }} value={form.endpoint_url} onChange={(e) => set('endpoint_url', e.target.value)} placeholder="http://host:port" />
                <button onClick={handleDiscover} disabled={discovering} className="discover-btn" style={{ whiteSpace: 'nowrap', flexShrink: 0, padding: '8px 14px', fontSize: '13px' }}>
                  {discovering ? 'Discovering…' : 'Discover'}
                </button>
              </div>
              {discoverError && <div style={{ fontSize: '11px', color: '#f87171', marginTop: '4px' }}>{discoverError}</div>}
              {form.agent_card_url && !discoverError && (
                <div style={{ fontSize: '11px', color: '#4edea3', marginTop: '4px' }}>
                  Card fetched — {form.skills.length} skill{form.skills.length !== 1 ? 's' : ''} discovered
                  {form.supports_streaming && ' · streaming'}{form.supports_push && ' · push'}
                </div>
              )}
            </Field>
            <Field label="Display Name">
              <input style={inputStyle} value={form.display_name} onChange={(e) => set('display_name', e.target.value)} placeholder="Echo Agent" />
            </Field>
            {!editing && (
              <Field label="Slug">
                <input style={inputStyle} value={form.slug} onChange={(e) => set('slug', e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, '_'))} placeholder="echo_agent" />
                <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', marginTop: '4px' }}>lowercase letters, numbers, underscores only</div>
              </Field>
            )}
            <Field label="Description (shown to LLM)">
              <textarea style={{ ...inputStyle, minHeight: '72px', resize: 'vertical', fontFamily: 'inherit' }} value={form.description} onChange={(e) => set('description', e.target.value)} placeholder="Echoes back any message it receives" />
            </Field>
            {form.skills.length > 0 && (
              <Field label={`Skills (${form.skills.length})`}>
                <div style={{ ...nestedSurface, borderRadius: '8px', padding: '8px 12px', maxHeight: '120px', overflowY: 'auto' }}>
                  {form.skills.map((s, i) => (
                    <div key={i} style={{ fontSize: '12px', color: 'var(--tm-text)', marginBottom: i < form.skills.length - 1 ? '6px' : 0 }}>
                      <span style={{ fontWeight: 600 }}>{s.name}</span>
                      {s.description && <span style={{ color: 'var(--tm-text-muted)' }}> — {s.description}</span>}
                    </div>
                  ))}
                </div>
              </Field>
            )}
            <Field label={editing ? 'Auth Token (leave blank to keep existing)' : 'Auth Token (optional)'}>
              <input style={inputStyle} type="password" value={form.auth_token} onChange={(e) => set('auth_token', e.target.value)} placeholder="Bearer token for the agent endpoint" />
            </Field>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '12px' }}>
              <Field label="Max Concurrency">
                <input style={inputStyle} type="number" min={1} max={20} value={form.max_concurrency} onChange={(e) => set('max_concurrency', Number(e.target.value))} />
              </Field>
              <Field label="Timeout (seconds)">
                <input style={inputStyle} type="number" min={5} max={300} value={form.timeout_seconds} onChange={(e) => set('timeout_seconds', Number(e.target.value))} />
              </Field>
            </div>
            <Field label="Icon (Material Symbols name, optional)">
              <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                <input style={{ ...inputStyle, flex: 1 }} value={form.icon} onChange={(e) => set('icon', e.target.value)} placeholder="e.g. hub, visibility, code — leave blank to auto-detect" />
                {form.icon && (
                  <span className="material-symbols-outlined" style={{ fontSize: '24px', color: '#00d1ff', flexShrink: 0 }}>{form.icon}</span>
                )}
              </div>
            </Field>
            <Field label="">
              <button
                type="button"
                onClick={() => set('enabled', !form.enabled)}
                style={{
                  display: 'flex', alignItems: 'center', gap: '10px',
                  padding: '8px 16px', borderRadius: '9px', border: 'none',
                  cursor: 'pointer', fontSize: '14px', fontWeight: 600,
                  background: form.enabled ? 'rgba(16,185,129,0.15)' : 'rgba(100,116,139,0.15)',
                  color: form.enabled ? '#34d399' : '#94a3b8',
                  transition: 'all 0.18s',
                }}
              >
                <span style={{
                  width: '32px', height: '18px', borderRadius: '9px', flexShrink: 0,
                  background: form.enabled ? '#34d399' : '#475569',
                  position: 'relative', display: 'inline-block',
                  transition: 'background 0.18s',
                }}>
                  <span style={{
                    position: 'absolute', top: '3px',
                    left: form.enabled ? '17px' : '3px',
                    width: '12px', height: '12px', borderRadius: '50%',
                    background: '#fff', transition: 'left 0.18s',
                  }} />
                </span>
                {form.enabled ? 'Enabled' : 'Disabled'}
              </button>
            </Field>
            {error && <div style={{ padding: '10px 12px', borderRadius: '8px', background: 'rgba(220,38,38,.08)', border: '1px solid rgba(220,38,38,.2)', color: '#f87171', fontSize: '13px', marginBottom: '16px' }}>{error}</div>}
            <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end', marginTop: '8px' }}>
              <button className="ghost-btn" onClick={() => setShowModal(false)} style={{ padding: '8px 20px', fontSize: '14px' }}>Cancel</button>
              <button onClick={handleSave} disabled={saving} style={{
                padding: '8px 22px', borderRadius: '9px', border: 'none',
                background: saving ? 'rgba(99,102,241,.5)' : 'var(--tm-accent)',
                color: '#fff', cursor: saving ? 'not-allowed' : 'pointer', fontSize: '14px', fontWeight: 600, opacity: saving ? 0.7 : 1,
              }}>{saving ? 'Saving…' : (editing ? 'Save Changes' : 'Create Agent')}</button>
            </div>
          </Modal>
        )}

        {/* ── Discover popup ── */}
        {discoverPopup && (() => {
          const { agent, result, diff } = discoverPopup;
          const newCard = (result.agent_card ?? {}) as Record<string, unknown>;
          const version = cardVersion(result.agent_card);
          const provider = cardProvider(result.agent_card);
          const docUrl = cardDocUrl(result.agent_card);
          const authSchemes = cardAuth(result.agent_card);
          const affectedOrchestrators = orchestrators.filter(o => o.allowed_agent_ids.includes(agent.id));

          return (
            <Modal wide title={`Agent Card — ${result.display_name || agent.display_name}`} onClose={() => setDiscoverPopup(null)}>
              <div style={{
                padding: '10px 14px', borderRadius: '9px', marginBottom: '20px',
                background: diff.hasChanges
                  ? 'linear-gradient(145deg, rgba(230,184,92,.10), rgba(180,131,9,.04))'
                  : 'linear-gradient(145deg, rgba(66,217,139,.10), rgba(42,181,109,.04))',
                border: `1px solid ${diff.hasChanges ? 'rgba(230,184,92,.28)' : 'rgba(66,217,139,.28)'}`,
                boxShadow: 'inset 0 1px 0 rgba(255,255,255,.025)',
                display: 'flex', alignItems: 'center', gap: '8px',
              }}>
                <span style={{ fontSize: '16px' }}>{diff.hasChanges ? '⚠️' : '✓'}</span>
                <span style={{ fontSize: '13px', fontWeight: 600, color: diff.hasChanges ? '#e6b85c' : '#4edea3' }}>
                  {diff.hasChanges ? 'Changes detected — review and save to update this agent' : 'Up to date — no changes since last sync'}
                </span>
              </div>

              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '24px' }}>
                <div>
                  <SectionLabel>Agent Info</SectionLabel>
                  <div style={{ ...nestedSurface, borderRadius: '8px', overflow: 'hidden', marginBottom: '16px' }}>
                    <DiffRow label="Name" changed={diff.displayName.changed} oldVal={diff.displayName.old} newVal={diff.displayName.new} />
                    {version && <DiffRow label="Version" changed={diff.version.changed} oldVal={diff.version.old} newVal={version} />}
                    {provider && <DiffRow label="Provider" changed={diff.provider.changed} oldVal={diff.provider.old} newVal={provider} />}
                    {docUrl && (
                      <div style={{ display: 'flex', gap: '8px', alignItems: 'center', padding: '6px 0 6px 0', borderBottom: '1px solid var(--tm-border)' }}>
                        <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)', minWidth: '110px' }}>Docs</span>
                        <a href={docUrl} target="_blank" rel="noreferrer" style={{ fontSize: '12px', color: '#60a5fa' }}>{docUrl}</a>
                      </div>
                    )}
                    <DiffRow label="Streaming" changed={diff.streaming.changed} oldVal={diff.streaming.old ? 'yes' : 'no'} newVal={diff.streaming.new ? 'yes' : 'no'} />
                    <DiffRow label="Push" changed={diff.push.changed} oldVal={diff.push.old ? 'yes' : 'no'} newVal={diff.push.new ? 'yes' : 'no'} />
                    {authSchemes.length > 0 && (
                      <div style={{ display: 'flex', gap: '8px', alignItems: 'center', padding: '6px 0' }}>
                        <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)', minWidth: '110px' }}>Auth</span>
                        <span style={{ fontSize: '12px', color: 'var(--tm-text)' }}>{authSchemes.join(', ')}</span>
                      </div>
                    )}
                  </div>

                  <SectionLabel>Card URL</SectionLabel>
                  <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', fontFamily: 'monospace', wordBreak: 'break-all', marginBottom: '16px' }}>
                    {result.agent_card_url}
                  </div>

                  {affectedOrchestrators.length > 0 && (
                    <>
                      <SectionLabel>Used by orchestrators</SectionLabel>
                      <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                        {affectedOrchestrators.map(o => (
                          <div key={o.id} style={{ fontSize: '12px', padding: '4px 8px', borderRadius: '6px', background: 'rgba(230,184,92,.08)', color: '#e6b85c', border: '1px solid rgba(230,184,92,.2)' }}>
                            {o.display_name || o.name}
                          </div>
                        ))}
                      </div>
                    </>
                  )}
                </div>

                <div>
                  <SectionLabel>Description</SectionLabel>
                  <div style={{
                    ...nestedSurface,
                    padding: '10px 12px', borderRadius: '8px', marginBottom: '16px',
                    border: `1px solid ${diff.description.changed ? 'rgba(66,217,139,.28)' : 'rgba(132,157,188,.12)'}`,
                  }}>
                    {diff.description.changed && (
                      <div style={{ fontSize: '11px', color: '#94a3b8', textDecoration: 'line-through', marginBottom: '6px', whiteSpace: 'pre-wrap' }}>{diff.description.old || '—'}</div>
                    )}
                    <div style={{ fontSize: '12px', color: diff.description.changed ? '#4edea3' : '#e2e8f0', whiteSpace: 'pre-wrap', lineHeight: 1.5 }}>
                      {result.description || '—'}
                    </div>
                  </div>

                  <SectionLabel>Skills ({result.skills.length}){diff.skills.changed && <span style={{ color: '#e6b85c', marginLeft: '6px', textTransform: 'none', fontSize: '10px' }}>changed</span>}</SectionLabel>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: '6px', maxHeight: '280px', overflowY: 'auto' }}>
                    {result.skills.length === 0 && <div style={{ fontSize: '12px', color: 'var(--tm-text-muted)' }}>No skills declared</div>}
                    {result.skills.map((s, i) => {
                      const skillCard = ((newCard.skills ?? []) as Record<string, unknown>[])[i] ?? {};
                      const inputModes = Array.isArray(skillCard.inputModes) ? (skillCard.inputModes as string[]) : [];
                      const outputModes = Array.isArray(skillCard.outputModes) ? (skillCard.outputModes as string[]) : [];
                      return (
                        <div key={i} style={{ ...nestedSurface, padding: '8px 10px', borderRadius: '8px' }}>
                          <div style={{ fontSize: '12px', fontWeight: 600, color: 'var(--tm-text)', marginBottom: '2px' }}>{s.name}</div>
                          {s.description && <div style={{ fontSize: '11px', color: '#cbd5e1', lineHeight: 1.4, marginBottom: '4px' }}>{s.description}</div>}
                          <div style={{ display: 'flex', gap: '4px', flexWrap: 'wrap' }}>
                            {(s.tags ?? []).map((t, ti) => (
                              <span key={ti} style={{ fontSize: '10px', padding: '1px 5px', borderRadius: '3px', background: 'rgba(167,139,250,.12)', color: '#a78bfa', border: '1px solid rgba(167,139,250,.18)' }}>{t}</span>
                            ))}
                            {inputModes.map((m, mi) => (
                              <span key={`in-${mi}`} style={{ fontSize: '10px', padding: '1px 5px', borderRadius: '3px', background: 'rgba(96,165,250,.10)', color: '#60a5fa', border: '1px solid rgba(96,165,250,.18)' }}>in:{m}</span>
                            ))}
                            {outputModes.map((m, mi) => (
                              <span key={`out-${mi}`} style={{ fontSize: '10px', padding: '1px 5px', borderRadius: '3px', background: 'rgba(78,222,163,.10)', color: '#4edea3', border: '1px solid rgba(78,222,163,.18)' }}>out:{m}</span>
                            ))}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                </div>
              </div>

              <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end', marginTop: '24px', paddingTop: '16px', borderTop: '1px solid rgba(132,158,190,.12)' }}>
                <button className="ghost-btn" onClick={() => setDiscoverPopup(null)} style={{ padding: '8px 20px', fontSize: '14px' }}>Close</button>
                {diff.hasChanges && (
                  <button
                    onClick={handleApplyDiscover}
                    disabled={applyingDiscover}
                    className="save-pulse"
                    style={{
                      padding: '8px 24px', borderRadius: '9px',
                      border: '1px solid rgba(167,139,250,.48)',
                      background: 'linear-gradient(145deg, rgba(167,139,250,.18), rgba(124,58,237,.10)), rgba(5,15,28,.80)',
                      boxShadow: '0 6px 18px rgba(124,58,237,.14), inset 0 1px 0 rgba(255,255,255,.08)',
                      color: '#c4b5fd', cursor: applyingDiscover ? 'not-allowed' : 'pointer',
                      fontSize: '14px', fontWeight: 700, opacity: applyingDiscover ? 0.7 : 1,
                    }}
                  >
                    {applyingDiscover ? 'Saving…' : 'Save Changes'}
                  </button>
                )}
              </div>
            </Modal>
          );
        })()}

        {/* ── Security scan detail modal ── */}
        {scanModal && (() => {
          const { agent, result } = scanModal;
          const rc = riskColors(result.risk);
          const ringColor = scoreRingColor(result.score);
          return (
            <Modal wide title="" onClose={() => setScanModal(null)}>
              <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '24px', marginTop: '-8px' }}>
                <div style={{
                  width: '40px', height: '40px', borderRadius: '11px', flexShrink: 0,
                  background: 'radial-gradient(circle at 30% 20%, rgba(40,215,238,.20), transparent 60%), linear-gradient(145deg, rgba(27,47,68,.96), rgba(8,20,35,.96))',
                  border: '1px solid rgba(40,215,238,.35)',
                  boxShadow: '0 5px 14px rgba(0,0,0,.28), 0 0 12px rgba(40,215,238,.10), inset 0 1px 0 rgba(255,255,255,.07)',
                  display: 'flex', alignItems: 'center', justifyContent: 'center',
                  fontSize: '20px',
                }}>🛡</div>
                <div>
                  <div style={{ fontSize: '11px', fontWeight: 700, color: 'var(--tm-text-muted)', textTransform: 'uppercase', letterSpacing: '0.07em', marginBottom: '2px' }}>Security Report</div>
                  <div style={{ fontSize: '17px', fontWeight: 700, color: 'var(--tm-text)', letterSpacing: '-0.01em' }}>{agent.display_name}</div>
                </div>
              </div>

              <div style={{
                display: 'flex', alignItems: 'center', gap: '16px', marginBottom: '24px',
                padding: '16px 18px', borderRadius: '12px',
                background: 'linear-gradient(145deg, rgba(255,255,255,.020) 0%, rgba(255,255,255,.006) 36%, rgba(0,0,0,.04) 100%), rgba(8,18,32,.80)',
                border: `1px solid ${rc.border}`,
                boxShadow: `0 6px 18px rgba(0,0,0,.22), 0 0 24px ${rc.glow}, inset 0 1px 0 rgba(255,255,255,.04)`,
              }}>
                <div className="score-ring" style={{
                  background: `radial-gradient(circle at 40% 30%, ${ringColor}22, transparent 60%), linear-gradient(145deg, rgba(20,38,60,.96), rgba(8,18,32,.96))`,
                  border: `2px solid ${ringColor}55`,
                }}>
                  <span style={{ fontSize: '24px', fontWeight: 800, color: ringColor, lineHeight: 1 }}>{result.score}</span>
                  <span style={{ fontSize: '9px', fontWeight: 700, color: `${ringColor}99`, textTransform: 'uppercase', letterSpacing: '0.04em' }}>/100</span>
                </div>
                <div style={{ flex: 1 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '8px' }}>
                    <span style={{
                      fontSize: '10px', fontWeight: 700, padding: '3px 10px', borderRadius: '5px', letterSpacing: '0.06em',
                      textTransform: 'uppercase', background: rc.bg, border: `1px solid ${rc.border}`,
                      boxShadow: 'inset 0 1px 0 rgba(255,255,255,.04)', color: rc.color,
                    }}>
                      {result.risk} risk
                    </span>
                  </div>
                  <p style={{ fontSize: '14px', color: '#e2e8f0', lineHeight: 1.55, fontWeight: 500, margin: 0 }}>
                    {result.summary}
                  </p>
                </div>
              </div>

              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '24px' }}>
                <div>
                  <SectionLabel>Findings</SectionLabel>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: '0' }}>
                    {result.findings.map((f, i) => {
                      const si = statusIcon(f.status);
                      const frc = riskColors(f.risk);
                      return (
                        <div key={i} className="finding-card" style={{ ...nestedSurface, borderRadius: '10px', padding: '11px 13px', marginBottom: '7px' }}>
                          <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '4px' }}>
                            <span style={{
                              width: '22px', height: '22px', borderRadius: '6px', flexShrink: 0,
                              background: `${si.color}18`, border: `1px solid ${si.color}30`,
                              display: 'flex', alignItems: 'center', justifyContent: 'center',
                              fontSize: '12px', color: si.color, fontWeight: 700,
                            }}>{si.icon}</span>
                            <span style={{ fontSize: '13px', fontWeight: 700, color: 'var(--tm-text)', flex: 1 }}>{f.label}</span>
                            <span style={{
                              fontSize: '9px', fontWeight: 700, padding: '2px 7px', borderRadius: '4px',
                              background: frc.bg, border: `1px solid ${frc.border}`, color: frc.color,
                              textTransform: 'uppercase', letterSpacing: '0.05em',
                            }}>{f.risk}</span>
                          </div>
                          <p style={{ fontSize: '12px', color: 'var(--tm-text-muted)', margin: '0 0 4px 30px', lineHeight: 1.5 }}>{f.detail}</p>
                          {f.recommendation !== 'No action needed.' && (
                            <p style={{ fontSize: '11px', color: '#60a5fa', margin: '0 0 0 30px', lineHeight: 1.4 }}>
                              <span style={{ opacity: 0.6 }}>→</span> {f.recommendation}
                            </p>
                          )}
                        </div>
                      );
                    })}
                  </div>
                </div>

                <div>
                  <SectionLabel>HTTP Probes</SectionLabel>
                  <div style={{ marginBottom: '20px' }}>
                    {[
                      { label: 'TLS', value: result.http_probes.tls },
                      { label: 'Auth Required', value: result.http_probes.auth_required },
                      { label: 'Reachable', value: result.http_probes.reachable ? 'pass' : 'fail' },
                    ].map(({ label, value }) => {
                      const pass = value === 'pass';
                      return (
                        <div key={label} className="probe-row" style={{ ...nestedSurface, borderRadius: '8px', marginBottom: '6px' }}>
                          <span style={{ fontSize: '12px', color: 'var(--tm-text-muted)', fontWeight: 500 }}>{label}</span>
                          <span style={{
                            fontSize: '10px', fontWeight: 700, padding: '2px 9px', borderRadius: '4px', letterSpacing: '0.04em',
                            background: pass
                              ? 'linear-gradient(145deg, rgba(66,217,139,.14), rgba(42,181,109,.06))'
                              : 'linear-gradient(145deg, rgba(220,38,38,.14), rgba(185,28,28,.06))',
                            border: `1px solid ${pass ? 'rgba(66,217,139,.24)' : 'rgba(220,38,38,.24)'}`,
                            boxShadow: 'inset 0 1px 0 rgba(255,255,255,.03)',
                            color: pass ? '#4edea3' : '#f87171',
                          }}>
                            {pass ? '✓ pass' : '✗ fail'}
                          </span>
                        </div>
                      );
                    })}
                  </div>

                  <SectionLabel>Scanned</SectionLabel>
                  <p style={{ fontSize: '12px', color: 'var(--tm-text-muted)', marginBottom: '20px', lineHeight: 1.5 }}>
                    {timeAgo(result.scanned_at)}<br />
                    <span style={{ fontSize: '11px', opacity: 0.7 }}>{new Date(result.scanned_at).toLocaleString()}</span>
                  </p>

                  <SectionLabel>Agent</SectionLabel>
                  <div style={{ ...nestedSurface, borderRadius: '8px', padding: '8px 12px' }}>
                    <div style={{ fontSize: '12px', color: 'var(--tm-text)', fontWeight: 600, marginBottom: '3px' }}>
                      <code style={{ background: 'rgba(40,215,238,.08)', border: '1px solid rgba(40,215,238,.15)', padding: '1px 6px', borderRadius: '4px', color: '#28d7ee', fontSize: '11px' }}>{agent.slug}</code>
                    </div>
                    <div style={{ fontSize: '11px', color: 'var(--tm-text-muted)', wordBreak: 'break-all', lineHeight: 1.4 }}>{agent.endpoint_url}</div>
                  </div>
                </div>
              </div>

              <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end', marginTop: '24px', paddingTop: '16px', borderTop: '1px solid rgba(132,158,190,.10)' }}>
                <button className="ghost-btn" onClick={() => setScanModal(null)} style={{ padding: '8px 20px', fontSize: '14px' }}>Close</button>
                <button
                  className="btn-primary-scan"
                  onClick={() => handleScan(agent)}
                  disabled={scanResults[agent.id] === 'scanning'}
                >
                  <span style={{ fontSize: '15px' }}>🛡</span>
                  {scanResults[agent.id] === 'scanning' ? 'Scanning…' : 'Re-scan'}
                </button>
              </div>
            </Modal>
          );
        })()}

        {/* ── Delete confirm ── */}
        {deleteTarget && (
          <Modal title="Delete Agent" onClose={() => setDeleteTarget(null)}>
            <p style={{ color: 'var(--tm-text)', marginBottom: '24px', lineHeight: 1.6 }}>
              Delete <strong style={{ color: '#f87171' }}>{deleteTarget.display_name}</strong>? This cannot be undone and will remove it from any orchestrators that use it.
            </p>
            <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end' }}>
              <button className="ghost-btn" onClick={() => setDeleteTarget(null)} style={{ padding: '8px 20px', fontSize: '14px' }}>Cancel</button>
              <button className="delete-btn" onClick={handleDelete} style={{ padding: '8px 20px', fontSize: '14px', fontWeight: 700 }}>Delete</button>
            </div>
          </Modal>
        )}
      </div>
    </AuthGuard>
  );
}
