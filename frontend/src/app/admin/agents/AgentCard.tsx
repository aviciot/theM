'use client';
import { useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/navigation';
import type { Agent, ScanResult } from '@/lib/api';
import { timeAgo, agentCategory, categoryBadgeStyle, categoryAccent, chromaGradient, agentIcon, riskColors } from './agentUtils';

export const nestedSurface: React.CSSProperties = {
  background: 'linear-gradient(145deg, rgba(255,255,255,.018), rgba(0,0,0,.05)), var(--tm-inset)',
  border: '1px solid rgba(132,157,188,.12)',
  boxShadow: 'inset 0 1px 0 rgba(255,255,255,.025), inset 0 -1px 0 rgba(0,0,0,.2)',
};

export const inputStyle: React.CSSProperties = {
  width: '100%', padding: '8px 12px', borderRadius: '8px',
  border: '1px solid var(--tm-input-border)',
  background: 'linear-gradient(145deg, rgba(255,255,255,.018), rgba(0,0,0,.05)), var(--tm-inset)',
  boxShadow: 'inset 0 1px 0 rgba(255,255,255,.025), inset 0 -1px 0 rgba(0,0,0,.2)',
  fontSize: '14px', color: 'var(--tm-text)', boxSizing: 'border-box',
};

// Module-level set — survives tab switches (component unmount/remount)
export const _inFlightScans = new Set<string>();

export function AgentCard({
  agent,
  scanResult,
  scanStep,
  testResult,
  isDiscovering,
  discoverError,
  discoverSuccess,
  onTest,
  onScan,
  onDiscover,
  onEdit,
  onDelete,
  onOpenScanModal,
  isDragOver,
  onDragStart,
  onDragOver,
  onDrop,
  onRemoveFromFolder,
}: {
  agent: Agent;
  scanResult: ScanResult | 'scanning' | undefined;
  scanStep?: string;
  testResult: { ok: boolean; latency_ms: number; detail: string } | 'testing' | undefined;
  isDiscovering: boolean;
  discoverError?: string;
  discoverSuccess?: boolean;
  onTest: () => void;
  onScan: () => void;
  onDiscover: () => void;
  onEdit: () => void;
  onDelete: () => void;
  onOpenScanModal: () => void;
  isDragOver?: boolean;
  onDragStart?: (e: React.DragEvent) => void;
  onDragOver?: (e: React.DragEvent) => void;
  onDrop?: (e: React.DragEvent) => void;
  onRemoveFromFolder?: () => void;
}) {
  const router = useRouter();
  const [copied, setCopied] = useState(false);
  const [showOverflow, setShowOverflow] = useState(false);
  const overflowRef = useRef<HTMLDivElement>(null);
  const isInternal = agent.tags?.includes('internal') ?? false;
  const isLocked = isInternal || (agent.tags?.includes('locked') ?? false);
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
    const text = agent.endpoint_url;
    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(text).then(() => {
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      });
    } else {
      const el = document.createElement('textarea');
      el.value = text;
      el.style.position = 'fixed';
      el.style.opacity = '0';
      document.body.appendChild(el);
      el.select();
      document.execCommand('copy');
      document.body.removeChild(el);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }
  }

  return (
    <article
      className={`glass-card chroma-card${scanResult === 'scanning' ? ' scanning-card' : ''}`}
      draggable
      onDragStart={(e) => {
        if ((e.target as HTMLElement).closest('button, input, a, textarea, select')) {
          e.preventDefault();
          return;
        }
        onDragStart?.(e);
      }}
      onDragOver={onDragOver}
      onDrop={onDrop}
      style={{
        padding: '22px', display: 'flex', flexDirection: 'column', gap: '14px',
        borderRadius: '20px', position: 'relative', cursor: 'grab',
        '--card-border': accent.color,
        '--card-gradient': isInternal
          ? 'linear-gradient(145deg, rgba(0,160,120,0.12) 0%, rgba(0,80,60,0.08) 40%, #0a0d14 100%)'
          : chromaGradient(accent.color),
        ...(isDragOver ? { borderColor: '#00d1ff', boxShadow: '0 0 0 2px rgba(0,209,255,0.25), 0 8px 32px rgba(0,0,0,0.4)' } : {}),
      } as React.CSSProperties}
    >
      {scanResult === 'scanning' && <div className="laser-beam" />}

      {/* ── Remove from folder button ── */}
      {onRemoveFromFolder && (
        <button
          onClick={(e) => { e.stopPropagation(); onRemoveFromFolder(); }}
          title="Remove from folder"
          style={{
            position: 'absolute', top: '10px', left: '10px', zIndex: 5,
            width: '20px', height: '20px', borderRadius: '50%',
            background: 'rgba(220,38,38,0.18)', border: '1px solid rgba(220,38,38,0.35)',
            color: '#f87171', fontSize: '11px', lineHeight: 1,
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            cursor: 'pointer', padding: 0,
            transition: 'background 150ms ease, border-color 150ms ease',
          }}
        >×</button>
      )}

      {/* ── Header row: icon + name/badges + overflow ── */}
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: '12px' }}>

        {/* Icon tile */}
        <div style={{
          width: '56px', height: '56px', flexShrink: 0, borderRadius: '14px',
          background: `radial-gradient(circle at 30% 25%, ${accent.glow}, transparent 65%),
                       linear-gradient(145deg, var(--tm-inset), var(--tm-inset-deep))`,
          border: `1px solid ${accent.border}`,
          boxShadow: `0 0 18px ${accent.glow}, inset 0 1px 0 var(--tm-card-border)`,
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          position: 'relative',
        }}>
          <span className="material-symbols-outlined" style={{ fontSize: '26px', color: accent.color }}>{icon}</span>
          {isLocked && (
            <span
              title={isInternal ? 'the-M system agent — cannot be deleted' : 'Locked — delete disabled'}
              className="material-symbols-outlined"
              style={{
                position: 'absolute', bottom: '-6px', right: '-6px',
                fontSize: '13px',
                color: isInternal ? '#a0f0d0' : '#94a3b8',
                background: 'var(--tm-inset-deep)',
                border: `1px solid ${isInternal ? 'rgba(160,240,208,0.35)' : 'rgba(148,163,184,0.3)'}`,
                borderRadius: '50%',
                width: '20px', height: '20px',
                display: 'flex', alignItems: 'center', justifyContent: 'center',
                boxShadow: `0 0 8px ${isInternal ? 'rgba(160,240,208,0.2)' : 'rgba(0,0,0,0.3)'}`,
              }}
            >lock</span>
          )}
        </div>

        {/* Name + status badges */}
        <div style={{ flex: 1, minWidth: 0, paddingTop: '2px' }}>
          <h3 style={{
            fontSize: '16px', fontWeight: 700, color: 'var(--tm-card-text)', margin: '0 0 6px 0',
            lineHeight: 1.2, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
          }}>{agent.display_name}</h3>

          <div style={{ display: 'flex', alignItems: 'center', gap: '6px', flexWrap: 'wrap' }}>
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

            <span style={{
              fontSize: '9px', fontWeight: 700, letterSpacing: '0.06em', textTransform: 'uppercase',
              padding: '2px 7px', borderRadius: '9999px', display: 'inline-block',
              ...catStyle,
            }}>{category}</span>

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

            {agent.runtime_definition_id != null && (
              <span title="Built in the-M canvas agent builder" style={{ display: 'inline-flex', alignItems: 'center', gap: 4, padding: '2px 7px', borderRadius: '9999px', background: 'rgba(139,92,246,0.1)', border: '1px solid rgba(139,92,246,0.3)' }}>
                <svg width="20" height="16" viewBox="0 0 1407 1118" style={{ opacity: 0.85, flexShrink: 0 }}>
                  <polygon points="88,77 184,146 244,191 281,217 336,259 355,272 358,272 367,267 372,266 379,262 391,258 433,239 440,237 473,222 513,206 520,202 546,192 555,187 558,187 446,102 433,91 421,83 403,68 397,65 392,60 331,15 318,4 274,19 264,21 246,28 239,29 217,37 214,37 211,39 201,41 189,46 186,46 154,57 151,57 148,59 141,60 138,62 104,73 101,73 98,75" fill="#c4b5fd"/>
                  <polygon points="1323,77 1313,75 1292,67 1289,67 1239,50 1236,50 1233,48 1230,48 1189,34 1176,31 1094,4 1085,12 1074,19 1053,36 959,106 855,187 876,196 881,197 973,237 980,239 1034,263 1048,268 1055,272 1059,272 1139,213 1146,209 1177,185 1188,178 1208,162 1284,107" fill="#c4b5fd"/>
                  <polygon points="70,97 70,334 71,335 72,350 76,365 104,429 108,435 180,486 184,490 245,534 345,609 339,293 305,269 281,250 252,230 182,177 179,176 153,156 150,155" fill="#c4b5fd"/>
                  <polygon points="1342,97 1252,162 1248,166 1152,236 1148,240 1126,255 1122,259 1112,265 1103,273 1074,293 1073,296 1073,317 1072,318 1072,355 1071,356 1071,415 1070,416 1070,461 1069,462 1069,526 1068,527 1067,609 1306,433 1325,392 1336,365 1341,343 1341,331 1342,330" fill="#c4b5fd"/>
                  <polygon points="682,361 576,210 381,292 532,410 577,395 580,395 586,392 595,390 613,384 616,382 622,381 664,367 667,365" fill="#c4b5fd"/>
                  <polygon points="732,361 803,384 806,386 809,386 831,394 834,394 860,404 863,404 881,410 1033,291 837,210 764,315 760,319 759,322 740,348" fill="#c4b5fd"/>
                  <polygon points="367,314 367,373 368,374 368,430 369,431 371,567 380,574 383,575 388,580 396,585 505,669 508,611 509,610 509,595 510,594 512,540 513,539 513,524 514,523 514,504 515,503 515,490 516,489 517,454 518,453 518,434 519,433 504,421 501,420 490,410 468,394 427,361 423,359 395,336 392,335" fill="#c4b5fd"/>
                  <polygon points="1046,314 894,433 895,456 896,457 896,475 897,476 897,494 898,495 898,513 899,514 901,561 902,562 902,579 903,580 903,594 904,595 906,650 907,651 907,666 908,669 934,648 971,621 1041,567" fill="#c4b5fd"/>
                  <polygon points="549,424 693,539 693,534 694,533 693,532 693,377 676,382 664,387 660,387 657,389 654,389 635,396 632,396" fill="#c4b5fd"/>
                  <polygon points="864,424 815,407 812,407 791,400 779,395 776,395 721,377 721,539 732,529 736,527 752,513" fill="#c4b5fd"/>
                  <polygon points="535,446 532,511 531,512 531,531 530,532 530,546 529,547 529,567 528,568 527,600 526,601 526,616 525,617 525,634 524,635 524,650 523,651 523,662 522,663 522,682 543,697 628,763 640,771 645,776 649,778 692,812 693,809 693,799 692,798 692,793 693,792 693,572 685,567 679,561 675,559 649,537 611,508 605,502 602,501" fill="#c4b5fd"/>
                  <polygon points="878,446 721,572 721,594 720,595 720,775 721,776 720,780 721,781 721,812 752,787 756,785 816,738 892,681 891,669 890,668 890,650 889,649 889,633 888,632 888,619 887,618 885,567 884,566 884,554 883,553 883,532 882,531 882,513 881,512" fill="#c4b5fd"/>
                  <polygon points="100,461 95,488 89,506 87,509 86,515 77,534 75,541 55,582 38,613 16,647 13,656 13,662 16,670 26,679 42,685 62,690 67,693 74,700 76,705 76,720 68,743 68,749 70,755 75,763 87,770 97,772 125,772 126,771 130,772 128,775 112,781 89,784 83,791 81,797 81,805 83,811 89,818 100,824 105,829 109,836 111,843 111,860 105,889 105,910 108,922 115,933 121,939 173,974 286,1057 326,1088 345,1105 345,641" fill="#c4b5fd"/>
                  <polygon points="1312,462 1273,489 1230,522 1227,523 1143,586 1067,641 1067,1106 1080,1093 1135,1050 1138,1049 1172,1023 1235,978 1239,974 1249,968 1253,964 1256,963 1270,952 1292,938 1301,928 1305,920 1307,912 1308,897 1307,896 1307,888 1301,858 1302,839 1307,829 1312,824 1323,818 1328,813 1331,806 1331,796 1330,792 1324,784 1311,783 1297,780 1286,776 1282,773 1284,771 1287,772 1316,772 1328,769 1335,765 1340,760 1344,750 1344,742 1336,717 1336,706 1339,699 1344,694 1353,689 1371,685 1386,679 1394,673 1399,663 1399,655 1397,649 1372,610 1339,546 1321,500 1321,497 1316,484" fill="#c4b5fd"/>
                </svg>
                <span style={{ fontSize: '9px', fontWeight: 700, color: '#c4b5fd', letterSpacing: '0.06em', textTransform: 'uppercase' }}>built by the-M</span>
              </span>
            )}
          </div>

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
              padding: '2px 8px', borderRadius: '9999px', maxWidth: '160px', overflow: 'hidden',
              background: 'rgba(0,209,255,0.08)', border: '1px solid rgba(0,209,255,0.3)', color: '#00d1ff',
              animation: 'pulse 1.6s ease-in-out infinite',
            }}>
              <span style={{ fontSize: '10px', flexShrink: 0 }}>🛡</span>
              <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {scanStep || 'Scanning…'}
              </span>
            </span>
          )}
        </div>

        {/* Three-dot overflow menu */}
        <div ref={overflowRef} style={{ position: 'relative', flexShrink: 0 }}>
          <button onClick={() => setShowOverflow(v => !v)} style={{
            width: '32px', height: '32px', borderRadius: '8px', cursor: 'pointer',
            background: 'var(--tm-btn-2-bg)', border: '1px solid var(--tm-filter-border)',
            color: 'var(--tm-card-text-muted)', display: 'flex', alignItems: 'center', justifyContent: 'center',
            transition: 'color 150ms ease, border-color 150ms ease',
          }}>
            <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor">
              <circle cx="8" cy="3" r="1.5"/><circle cx="8" cy="8" r="1.5"/><circle cx="8" cy="13" r="1.5"/>
            </svg>
          </button>
          {showOverflow && (
            <div style={{
              position: 'absolute', top: '36px', right: 0, zIndex: 20,
              background: 'var(--tm-menu-bg)', border: '1px solid rgba(255,255,255,0.08)',
              borderRadius: '10px', overflow: 'hidden', minWidth: '130px',
              boxShadow: '0 8px 28px rgba(0,0,0,0.5)',
            }} onClick={() => setShowOverflow(false)}>
              <button onClick={onEdit} style={{ width: '100%', padding: '9px 14px', textAlign: 'left', background: 'none', border: 'none', color: 'var(--tm-card-text-hint)', fontSize: '12px', cursor: 'pointer' }}>
                ✎ Edit
              </button>
              <div style={{ height: '1px', background: 'var(--tm-divider)', margin: '0 8px' }} />
              {isLocked ? (
                <div style={{ width: '100%', padding: '9px 14px', textAlign: 'left', color: 'rgba(148,163,184,0.45)', fontSize: '12px', display: 'flex', alignItems: 'center', gap: '6px', cursor: 'default' }}>
                  <span className="material-symbols-outlined" style={{ fontSize: '12px' }}>lock</span>
                  Locked
                </div>
              ) : (
                <button onClick={onDelete} style={{ width: '100%', padding: '9px 14px', textAlign: 'left', background: 'none', border: 'none', color: '#f87171', fontSize: '12px', cursor: 'pointer' }}>
                  ✕ Delete
                </button>
              )}
            </div>
          )}
        </div>
      </div>

      {/* ── Description ── */}
      <p style={{
        fontSize: '13px', color: 'var(--tm-card-text-muted)', lineHeight: 1.55, margin: 0,
        display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden',
        minHeight: '40px',
      }}>
        {agent.description || <span style={{ opacity: 0.35 }}>No description</span>}
      </p>

      {/* ── Stats tiles ── */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '8px' }}>
        <div style={{ padding: '10px 12px', borderRadius: '10px', background: 'var(--tm-inset-deep)', border: '1px solid var(--tm-divider)', display: 'flex', alignItems: 'center', gap: '8px' }}>
          <span className="material-symbols-outlined" style={{ fontSize: '16px', color: 'var(--tm-card-text-muted)', flexShrink: 0 }}>hub</span>
          <div>
            <p style={{ fontSize: '15px', fontWeight: 700, color: 'var(--tm-card-text)', margin: 0, lineHeight: 1 }}>
              {agent.skills && agent.skills.length > 0 ? agent.skills.length : '—'}
            </p>
            <p style={{ fontSize: '9px', color: 'var(--tm-card-text-muted)', textTransform: 'uppercase', fontWeight: 700, letterSpacing: '0.08em', margin: '2px 0 0 0' }}>skills</p>
          </div>
        </div>
        <div style={{ padding: '10px 12px', borderRadius: '10px', background: 'var(--tm-inset-deep)', border: '1px solid var(--tm-divider)', display: 'flex', alignItems: 'center', gap: '8px' }}>
          <span className="material-symbols-outlined" style={{ fontSize: '16px', color: 'var(--tm-card-text-muted)', flexShrink: 0 }}>sync</span>
          <div>
            <p style={{ fontSize: '13px', fontWeight: 700, color: 'var(--tm-card-text)', margin: 0, lineHeight: 1, whiteSpace: 'nowrap' }}>
              {agent.card_fetched_at ? timeAgo(agent.card_fetched_at) : '—'}
            </p>
            <p style={{ fontSize: '9px', color: 'var(--tm-card-text-muted)', textTransform: 'uppercase', fontWeight: 700, letterSpacing: '0.08em', margin: '2px 0 0 0' }}>last sync</p>
          </div>
        </div>
        <div style={{ padding: '10px 12px', borderRadius: '10px', background: 'var(--tm-inset-deep)', border: '1px solid var(--tm-divider)', display: 'flex', alignItems: 'center', gap: '8px' }}>
          <span className="material-symbols-outlined" style={{ fontSize: '16px', color: 'var(--tm-card-text-muted)', flexShrink: 0 }}>person</span>
          <div>
            <p style={{ fontSize: '13px', fontWeight: 700, color: 'var(--tm-card-text)', margin: 0, lineHeight: 1, whiteSpace: 'nowrap' }}>
              {agent.created_by_username || '—'}
            </p>
            <p style={{ fontSize: '9px', color: 'var(--tm-card-text-muted)', textTransform: 'uppercase', fontWeight: 700, letterSpacing: '0.08em', margin: '2px 0 0 0' }}>created by</p>
          </div>
        </div>
      </div>

      {/* ── Endpoint field ── */}
      <div>
        <p style={{ fontSize: '9px', fontWeight: 700, color: 'var(--tm-card-text-muted)', textTransform: 'uppercase', letterSpacing: '0.08em', margin: '0 0 5px 0' }}>Endpoint</p>
        <div style={{ display: 'flex', alignItems: 'center', background: 'var(--tm-inset-deep)', border: '1px solid var(--tm-divider)', borderRadius: '8px', padding: '7px 10px', gap: '6px' }}>
          <span style={{ fontSize: '11px', color: 'var(--tm-card-text-hint)', fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: 1 }}>
            {agent.endpoint_url || '—'}
          </span>
          <button onClick={copyEndpoint} title="Copy" style={{
            background: 'none', border: 'none', cursor: 'pointer', flexShrink: 0, padding: '2px 4px',
            color: copied ? '#34d399' : 'var(--tm-card-text-muted)', fontSize: '14px', transition: 'color 150ms ease',
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

      {/* ── Action buttons ── */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: '8px', marginTop: 'auto', paddingTop: '4px' }}>
        <button onClick={onTest} disabled={testResult === 'testing'} className="card-action-btn card-action-btn--primary">
          <span className="material-symbols-outlined" style={{ fontSize: '14px' }}>play_arrow</span>
          {testResult === 'testing' ? 'Testing…' : 'Test'}
        </button>
        <button onClick={onDiscover} disabled={isDiscovering} className={`card-action-btn card-action-btn--secondary${isDiscovering ? ' is-loading' : ''}`}>
          <span className={`material-symbols-outlined${isDiscovering ? ' spin' : ''}`} style={{ fontSize: '14px' }}>
            {isDiscovering ? 'sync' : 'radar'}
          </span>
          {isDiscovering ? 'Discovering…' : 'Discover'}
        </button>
        <button onClick={onScan} disabled={scanResult === 'scanning'} className="card-action-btn card-action-btn--scan" title="Security scan">
          <span className="material-symbols-outlined" style={{ fontSize: '14px' }}>security</span>
          {scanResult === 'scanning' ? 'Scanning…' : 'Scan'}
        </button>
      </div>

      {agent.runtime_definition_id && (
        <button
          onClick={() => router.push('/admin/agents/builder?id=' + agent.runtime_definition_id)}
          className="card-action-btn card-action-btn--secondary"
          title="Open in the-M canvas agent builder"
          style={{ width: '100%', marginTop: '6px' }}
        >
          <span className="material-symbols-outlined" style={{ fontSize: '14px' }}>edit_square</span>
          Edit in Builder
        </button>
      )}

      {discoverSuccess && (
        <div style={{ marginTop: '8px', padding: '6px 10px', borderRadius: '6px', background: 'rgba(16,185,129,0.08)', border: '1px solid rgba(16,185,129,0.2)', color: '#34d399', fontSize: '11px', lineHeight: 1.4, display: 'flex', alignItems: 'center', gap: '5px' }}>
          <span className="material-symbols-outlined" style={{ fontSize: '13px' }}>check_circle</span>
          Agent card synced — skills and last sync updated
        </div>
      )}
      {!discoverSuccess && discoverError && (
        <div style={{ marginTop: '8px', padding: '6px 10px', borderRadius: '6px', background: 'rgba(220,38,38,0.08)', border: '1px solid rgba(220,38,38,0.2)', color: '#f87171', fontSize: '11px', lineHeight: 1.4, display: 'flex', alignItems: 'center', gap: '5px' }}>
          <span className="material-symbols-outlined" style={{ fontSize: '13px' }}>error</span>
          {discoverError}
        </div>
      )}
    </article>
  );
}
