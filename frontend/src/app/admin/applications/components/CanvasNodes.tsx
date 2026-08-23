'use client';
import { Handle, Position, useReactFlow, type NodeTypes } from '@xyflow/react';
import type { EntryPointData, OrchestratorData, AgentData, MiddlewareData } from '../types';
import { C } from '../constants';
import { agentIconForLibrary } from './CanvasHelpers';

// ── Tiny the-M logo badge for internal nodes ──────────────────────────────────
function InternalMBadge() {
  return (
    <div title="Internal the-M system component" style={{
      position: 'absolute', top: 6, left: 8,
      display: 'flex', alignItems: 'center', gap: 4,
      pointerEvents: 'none',
    }}>
      <svg width="14" height="11" viewBox="0 0 1407 1118" style={{ opacity: 0.55, flexShrink: 0 }}>
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
      <span style={{ fontSize: 8, fontWeight: 700, color: '#a0f0d0', letterSpacing: 0.8, textTransform: 'uppercase', opacity: 0.7 }}>internal</span>
    </div>
  );
}

// ── EntryPointNode ─────────────────────────────────────────────────────────────
export function EntryPointNode({ id, data, selected }: { id: string; data: EntryPointData & { _scanning?: boolean; _error?: boolean; _shake?: boolean; _errorMsg?: string }; selected?: boolean }) {
  const { deleteElements } = useReactFlow();
  const epKind = data.epType || (data as unknown as Record<string, unknown>).protocol as string | undefined;
  const slugMissing = !data.slug;
  const hasError = data._error || data._shake;
  const isVoice = epKind === 'voice';
  const accent = hasError ? '#f87171' : slugMissing ? '#f59e0b' : isVoice ? C.amber : C.cyan;
  const EP_MS_ICON: Record<string, string> = { websocket: 'bolt', sse: 'stream', webrtc: 'videocam', a2a: 'robot_2', voice: 'mic' };
  const msIcon = EP_MS_ICON[epKind ?? ''] ?? 'bolt';
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}
      title={data._errorMsg || undefined}>
      {selected && (
        <button
          className="nodrag"
          onClick={(e) => { e.stopPropagation(); deleteElements({ nodes: [{ id }] }); }}
          style={{
            position: 'absolute', top: -8, right: -8,
            width: 18, height: 18, borderRadius: '50%',
            background: '#f87171', border: '2px solid #051424',
            color: '#fff', fontSize: 10, fontWeight: 700,
            cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
            lineHeight: 1, padding: 0, zIndex: 10,
          }}
          title="Delete node (or press Delete key)"
        >✕</button>
      )}
      <div
        className={`${hasError ? 'node-error-ring' : ''} ${data._shake ? 'node-shake' : ''}`}
        style={{
          width: 56, height: 56, borderRadius: '50%',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          background: selected ? `rgba(0,240,255,0.10)` : data._scanning ? 'rgba(0,240,255,0.08)' : 'transparent',
          border: selected ? `2px solid ${accent}` : hasError ? '2px solid #f87171' : '2px solid transparent',
          boxShadow: selected ? `0 0 14px rgba(0,240,255,0.35), inset 0 0 8px rgba(0,240,255,0.08)` : data._scanning ? '0 0 20px rgba(0,240,255,0.5)' : 'none',
          transition: 'all 0.18s ease',
        }}>
        <span className="material-symbols-outlined" style={{ fontSize: 28, color: accent, transition: 'all 0.18s' }}>{msIcon}</span>
      </div>
      <div style={{ marginTop: 6, textAlign: 'center' }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: selected ? '#fff' : C.text, lineHeight: 1.3, transition: 'color 0.18s' }}>
          {data.label || (epKind === 'sse' ? 'SSE' : epKind === 'voice' ? 'Voice' : epKind === 'webrtc' ? 'WebRTC' : epKind === 'a2a' ? 'A2A' : 'WebSocket')}
        </div>
        {data.slug ? (
          <div style={{ fontSize: 10, color: C.cyan, fontFamily: 'JetBrains Mono, monospace', opacity: 0.8, marginTop: 1 }}>{data.slug}</div>
        ) : (
          <div style={{ fontSize: 10, color: '#f59e0b', fontWeight: 600, marginTop: 1 }}>⚠ slug required</div>
        )}
      </div>
      <Handle type="source" position={Position.Bottom} style={{ background: C.cyan, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
    </div>
  );
}

const INTERNAL_ORCHESTRATOR_NAMES = new Set(['workflow_advisor']);

// ── OrchestratorNode ──────────────────────────────────────────────────────────
export function OrchestratorNode({ id, data, selected }: { id: string; data: OrchestratorData & { _scanning?: boolean; _error?: boolean; _shake?: boolean; _errorMsg?: string }; selected?: boolean }) {
  const { deleteElements } = useReactFlow();
  const isInternal = INTERNAL_ORCHESTRATOR_NAMES.has(data.name);
  const hasError = data._error || data._shake;
  const accent = hasError ? '#f87171' : isInternal ? '#a0f0d0' : C.purple;
  const selGlow = isInternal ? 'rgba(160,240,208,0.35)' : 'rgba(208,188,255,0.35)';
  const selBg   = isInternal ? 'rgba(160,240,208,0.10)' : 'rgba(208,188,255,0.10)';
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}
      title={data._errorMsg || undefined}>
      {selected && (
        <button
          className="nodrag"
          onClick={(e) => { e.stopPropagation(); deleteElements({ nodes: [{ id }] }); }}
          style={{
            position: 'absolute', top: -8, right: -8,
            width: 18, height: 18, borderRadius: '50%',
            background: '#f87171', border: '2px solid #051424',
            color: '#fff', fontSize: 10, fontWeight: 700,
            cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
            lineHeight: 1, padding: 0, zIndex: 10,
          }}
          title="Delete node (or press Delete key)"
        >✕</button>
      )}
      <Handle type="target" position={Position.Top} style={{ background: accent, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
      <div
        className={`${hasError ? 'node-error-ring' : ''} ${data._shake ? 'node-shake' : ''}`}
        style={{
          width: 56, height: 56, borderRadius: '50%',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          background: selected ? selBg : data._scanning ? 'rgba(0,240,255,0.08)' : 'transparent',
          border: selected ? `2px solid ${accent}` : hasError ? '2px solid #f87171' : '2px solid transparent',
          boxShadow: selected ? `0 0 14px ${selGlow}, inset 0 0 8px ${selGlow}` : data._scanning ? '0 0 20px rgba(0,240,255,0.5)' : 'none',
          transition: 'all 0.18s ease',
        }}>
        <span className="material-symbols-outlined" style={{ fontSize: 28, color: accent, transition: 'all 0.18s' }}>hub</span>
      </div>
      <div style={{ marginTop: 6, textAlign: 'center', maxWidth: 120 }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: selected ? '#fff' : C.text, lineHeight: 1.3, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', transition: 'color 0.18s' }}>
          {data.displayName}
        </div>
      </div>
      <Handle type="source" position={Position.Bottom} style={{ background: accent, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
    </div>
  );
}

// ── AgentNode ─────────────────────────────────────────────────────────────────
export function AgentNode({ id, data, selected }: { id: string; data: AgentData & { _scanning?: boolean; _error?: boolean; _shake?: boolean; _errorMsg?: string }; selected?: boolean }) {
  const { deleteElements } = useReactFlow();
  const isInternal = data.tags?.includes('internal') ?? false;
  const hasError = data._error || data._shake;
  const accent = hasError ? '#f87171' : isInternal ? '#a0f0d0' : C.green;
  const selGlow = isInternal ? 'rgba(160,240,208,0.35)' : 'rgba(74,222,128,0.35)';
  const selBg   = isInternal ? 'rgba(160,240,208,0.10)' : 'rgba(74,222,128,0.10)';
  const displayName = (data as unknown as Record<string, unknown>).display_name as string | undefined || data.displayName;
  const icon = data.icon || agentIconForLibrary({ slug: data.name, icon: data.icon } as any);
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}
      title={data._errorMsg || undefined}>
      {selected && (
        <button
          className="nodrag"
          onClick={(e) => { e.stopPropagation(); deleteElements({ nodes: [{ id }] }); }}
          style={{
            position: 'absolute', top: -8, right: -8,
            width: 18, height: 18, borderRadius: '50%',
            background: '#f87171', border: '2px solid #051424',
            color: '#fff', fontSize: 10, fontWeight: 700,
            cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
            lineHeight: 1, padding: 0, zIndex: 10,
          }}
          title="Delete node (or press Delete key)"
        >✕</button>
      )}
      <Handle type="target" position={Position.Top} style={{ background: accent, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
      <div
        className={`${hasError ? 'node-error-ring' : ''} ${data._shake ? 'node-shake' : ''}`}
        style={{
          width: 56, height: 56, borderRadius: '50%',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          background: selected ? selBg : data._scanning ? 'rgba(0,240,255,0.08)' : 'transparent',
          border: selected ? `2px solid ${accent}` : hasError ? '2px solid #f87171' : '2px solid transparent',
          boxShadow: selected ? `0 0 14px ${selGlow}, inset 0 0 8px ${selGlow}` : data._scanning ? '0 0 20px rgba(0,240,255,0.5)' : 'none',
          transition: 'all 0.18s ease',
        }}>
        <span className="material-symbols-outlined" style={{ fontSize: 28, color: accent, transition: 'all 0.18s' }}>{icon}</span>
      </div>
      <div style={{ marginTop: 6, textAlign: 'center', maxWidth: 110 }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: selected ? '#fff' : C.text, lineHeight: 1.3, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', transition: 'color 0.18s' }}>
          {displayName}
        </div>
      </div>
    </div>
  );
}

// ── MiddlewareNode ────────────────────────────────────────────────────────────
export function MiddlewareNode({ id, data, selected }: { id: string; data: MiddlewareData & { _scanning?: boolean; _error?: boolean; _shake?: boolean; _errorMsg?: string }; selected?: boolean }) {
  const { deleteElements } = useReactFlow();
  const hasError = data._error || data._shake;
  const accent = hasError ? '#f87171' : C.amber;
  const selGlow = 'rgba(245,158,11,0.35)';
  const selBg   = 'rgba(245,158,11,0.10)';
  const icon = data.kind === 'guard' ? 'shield' : 'bolt';
  return (
    <div style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', fontFamily: 'Inter, sans-serif', cursor: 'default' }}
      title={data._errorMsg || undefined}>
      {selected && (
        <button
          className="nodrag"
          onClick={(e) => { e.stopPropagation(); deleteElements({ nodes: [{ id }] }); }}
          style={{
            position: 'absolute', top: -8, right: -8,
            width: 18, height: 18, borderRadius: '50%',
            background: '#f87171', border: '2px solid #051424',
            color: '#fff', fontSize: 10, fontWeight: 700,
            cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
            lineHeight: 1, padding: 0, zIndex: 10,
          }}
          title="Delete node (or press Delete key)"
        >✕</button>
      )}
      <Handle type="target" position={Position.Top} style={{ background: accent, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
      <div
        className={`${hasError ? 'node-error-ring' : ''} ${data._shake ? 'node-shake' : ''}`}
        style={{
          width: 56, height: 56, borderRadius: '50%',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          background: selected ? selBg : data._scanning ? 'rgba(245,158,11,0.08)' : 'transparent',
          border: selected ? `2px solid ${accent}` : hasError ? '2px solid #f87171' : '2px solid transparent',
          boxShadow: selected ? `0 0 14px ${selGlow}, inset 0 0 8px ${selGlow}` : data._scanning ? C.amberGlow : 'none',
          transition: 'all 0.18s ease',
        }}>
        <span className="material-symbols-outlined" style={{ fontSize: 28, color: accent, transition: 'all 0.18s' }}>{icon}</span>
      </div>
      <div style={{ marginTop: 6, textAlign: 'center', maxWidth: 110 }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: selected ? '#fff' : C.text, lineHeight: 1.3, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', transition: 'color 0.18s' }}>
          {data.displayName}
        </div>
        <div style={{ fontSize: 9, color: accent, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.8, opacity: 0.8 }}>
          {data.kind}
        </div>
      </div>
      <Handle type="source" position={Position.Bottom} style={{ background: accent, border: `2px solid ${C.bg}`, width: 8, height: 8 }} />
    </div>
  );
}

// ── NODE_TYPES — must live here since it references the node components ────────
export const NODE_TYPES: NodeTypes = {
  entryPoint: EntryPointNode as any,
  orchestrator: OrchestratorNode as any,
  agent: AgentNode as any,
  middleware: MiddlewareNode as any,
};
