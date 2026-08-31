import type { Agent } from '@/lib/api';

export function timeAgo(iso: string): string {
  const diffMs = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(diffMs / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

export function cardVersion(card: Record<string, unknown> | null | undefined): string {
  return card ? String(card.version ?? '') : '';
}
export function cardProvider(card: Record<string, unknown> | null | undefined): string {
  if (!card?.provider || typeof card.provider !== 'object') return '';
  return String((card.provider as Record<string, unknown>).organization ?? '');
}
export function cardDocUrl(card: Record<string, unknown> | null | undefined): string {
  return card ? String(card.documentationUrl ?? '') : '';
}
export function cardAuth(card: Record<string, unknown> | null | undefined): string[] {
  if (!card?.authentication || !Array.isArray(card.authentication)) return [];
  return (card.authentication as unknown[]).map(a => typeof a === 'string' ? a : String((a as Record<string, unknown>).scheme ?? a));
}

export function agentCategory(agent: Agent): string {
  if (agent.category) return agent.category;
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

export function categoryBadgeStyle(category: string): React.CSSProperties {
  switch (category) {
    case 'A2A':      return { background: 'rgba(99,102,241,0.18)', color: '#818cf8', border: '1px solid rgba(99,102,241,0.3)' };
    case 'Research': return { background: 'rgba(168,85,247,0.18)', color: '#c084fc', border: '1px solid rgba(168,85,247,0.3)' };
    case 'Coding':   return { background: 'rgba(0,209,255,0.12)',  color: '#00d1ff', border: '1px solid rgba(0,209,255,0.28)' };
    case 'Vision':   return { background: 'rgba(59,130,246,0.18)', color: '#60a5fa', border: '1px solid rgba(59,130,246,0.3)' };
    case 'Security': return { background: 'rgba(245,158,11,0.15)', color: '#fbbf24', border: '1px solid rgba(245,158,11,0.28)' };
    default:         return { background: 'rgba(100,116,139,0.18)', color: '#94a3b8', border: '1px solid rgba(100,116,139,0.28)' };
  }
}

export function categoryAccent(category: string): { color: string; glow: string; border: string } {
  switch (category) {
    case 'A2A':      return { color: '#818cf8', glow: 'rgba(99,102,241,0.25)',  border: 'rgba(99,102,241,0.45)' };
    case 'Coding':   return { color: '#00d1ff', glow: 'rgba(0,209,255,0.22)',   border: 'rgba(0,209,255,0.42)' };
    case 'Vision':   return { color: '#60a5fa', glow: 'rgba(59,130,246,0.22)',  border: 'rgba(59,130,246,0.42)' };
    case 'Research': return { color: '#c084fc', glow: 'rgba(168,85,247,0.22)',  border: 'rgba(168,85,247,0.42)' };
    case 'Security': return { color: '#fbbf24', glow: 'rgba(245,158,11,0.22)',  border: 'rgba(245,158,11,0.42)' };
    default:         return { color: '#94a3b8', glow: 'rgba(100,116,139,0.18)', border: 'rgba(100,116,139,0.35)' };
  }
}

export function chromaGradient(accentColor: string): string {
  return `linear-gradient(145deg, ${accentColor}22 0%, ${accentColor}08 40%, #0a0d14 100%)`;
}

export function agentIcon(agent: Agent, category: string): string {
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

export function riskColors(risk: 'low' | 'medium' | 'high') {
  if (risk === 'low') return { bg: 'linear-gradient(145deg, rgba(66,217,139,.14) 0%, rgba(42,181,109,.08) 100%)', border: 'rgba(66,217,139,.28)', color: '#4edea3', glow: 'rgba(42,181,109,.18)' };
  if (risk === 'medium') return { bg: 'linear-gradient(145deg, rgba(230,184,92,.14) 0%, rgba(180,131,9,.08) 100%)', border: 'rgba(230,184,92,.28)', color: '#e6b85c', glow: 'rgba(180,131,9,.18)' };
  return { bg: 'linear-gradient(145deg, rgba(220,38,38,.14) 0%, rgba(185,28,28,.08) 100%)', border: 'rgba(220,38,38,.28)', color: '#f87171', glow: 'rgba(220,38,38,.18)' };
}

export function statusIcon(status: 'pass' | 'fail' | 'warn') {
  if (status === 'pass') return { icon: '✓', color: '#4edea3' };
  if (status === 'warn') return { icon: '⚠', color: '#e6b85c' };
  return { icon: '✗', color: '#f87171' };
}

export function scoreRingColor(score: number) {
  if (score >= 75) return '#4edea3';
  if (score >= 45) return '#e6b85c';
  return '#f87171';
}
