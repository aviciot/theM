'use client';
import { useRouter } from 'next/navigation';
import { themApi, type OrchestratorFull } from '@/lib/api';
import { providerColor, providerIcon, CYAN, PURPLE, GREEN, TEXT, MUTED, BORDER } from './orchestratorConstants';

export function OrchestratorCard({
  o,
  onEdit,
  onDelete,
  onReload,
}: {
  o: OrchestratorFull;
  onEdit: (o: OrchestratorFull) => void;
  onDelete: (o: OrchestratorFull) => void;
  onReload: () => void;
}) {
  const router = useRouter();
  const isInternal = o.name === 'workflow_advisor';
  const pColor  = isInternal ? '#a0f0d0' : providerColor(o.llm_provider);
  const pGlow   = `${pColor}38`;
  const pBorder = `${pColor}70`;
  const agentCount = o.allowed_agent_ids?.length ?? 0;

  return (
    <div
      className="orch-glass-card"
      style={{
        borderRadius: 20, display: 'flex', flexDirection: 'column', position: 'relative',
        ...(isInternal ? { background: 'linear-gradient(160deg, rgba(0,160,120,0.08) 0%, rgba(0,80,60,0.06) 100%), var(--tm-card)' } : {}),
      }}
    >
      {/* Card body — click to edit */}
      <div
        style={{ padding: '22px 22px 16px', flex: 1, display: 'flex', flexDirection: 'column', gap: 14, cursor: 'pointer' }}
        onClick={() => onEdit(o)}
      >
        {/* Header: icon + name + delete */}
        <div style={{ display: 'flex', alignItems: 'flex-start', gap: 14 }}>
          <div style={{
            width: 56, height: 56, borderRadius: 14, flexShrink: 0,
            background: `radial-gradient(circle at 30% 25%, ${pGlow}, transparent 65%),
                         linear-gradient(145deg, rgba(20,32,52,0.96), rgba(8,16,30,0.96))`,
            border: `1px solid ${pBorder}`,
            boxShadow: `0 0 18px ${pGlow}, inset 0 1px 0 rgba(255,255,255,0.07)`,
            display: 'flex', alignItems: 'center', justifyContent: 'center',
          }}>
            <span className="material-symbols-outlined" style={{ fontSize: 26, color: pColor }}>
              {providerIcon(o.llm_provider)}
            </span>
          </div>

          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontWeight: 700, fontSize: 16, color: TEXT, fontFamily: 'Geist, sans-serif', marginBottom: 6, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
              {o.display_name}
            </div>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, alignItems: 'center' }}>
              <span style={{
                display: 'inline-flex', alignItems: 'center', gap: 5,
                padding: '2px 8px', borderRadius: 20, fontSize: 11, fontWeight: 700,
                background: o.enabled ? 'rgba(52,211,153,0.1)' : 'rgba(248,113,113,0.1)',
                color: o.enabled ? GREEN : '#f87171',
                border: `1px solid ${o.enabled ? 'rgba(52,211,153,0.3)' : 'rgba(248,113,113,0.3)'}`,
              }}>
                {o.enabled && <span style={{ width: 5, height: 5, borderRadius: '50%', background: GREEN, display: 'inline-block', boxShadow: `0 0 5px ${GREEN}` }} />}
                {o.enabled ? 'live' : 'disabled'}
              </span>
              {o.llm_provider && (
                <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4, padding: '2px 8px', borderRadius: 20, fontSize: 11, fontWeight: 700, background: 'rgba(255,255,255,0.04)', color: pColor, border: `1px solid ${pBorder}` }}>
                  {o.llm_provider}
                </span>
              )}
              {isInternal && (
                <span title="Internal the-M system component" style={{ display: 'inline-flex', alignItems: 'center', gap: 4, padding: '2px 7px', borderRadius: 20, background: 'rgba(160,240,208,0.1)', border: '1px solid rgba(160,240,208,0.25)' }}>
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
                  <span style={{ fontSize: 9, fontWeight: 700, color: '#a0f0d0', letterSpacing: '0.06em', textTransform: 'uppercase' }}>internal</span>
                </span>
              )}
            </div>
            <div style={{ fontSize: 11, color: MUTED, fontFamily: 'JetBrains Mono, monospace', marginTop: 5 }}>{o.name}</div>
          </div>

          <div style={{ position: 'relative', flexShrink: 0 }} onClick={e => e.stopPropagation()}>
            <button
              onClick={() => onDelete(o)}
              title="Delete orchestrator"
              style={{ width: 32, height: 32, borderRadius: 8, cursor: 'pointer', background: 'rgba(30,41,59,0.5)', border: '1px solid rgba(255,255,255,0.06)', color: 'var(--tm-card-text-muted)', display: 'flex', alignItems: 'center', justifyContent: 'center', transition: 'color 150ms ease, border-color 150ms ease' }}
              onMouseEnter={e => { e.currentTarget.style.color = '#f87171'; e.currentTarget.style.borderColor = 'rgba(248,113,113,0.4)'; }}
              onMouseLeave={e => { e.currentTarget.style.color = 'var(--tm-card-text-muted)'; e.currentTarget.style.borderColor = 'rgba(255,255,255,0.06)'; }}
            >
              <span className="material-symbols-outlined" style={{ fontSize: 16 }}>delete</span>
            </button>
          </div>
        </div>

        {/* Stat tiles */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
          <div style={{ padding: '8px 12px', borderRadius: 10, background: 'var(--tm-filter-bg)', border: `1px solid ${BORDER}`, display: 'flex', alignItems: 'center', gap: 8 }}>
            <span className="material-symbols-outlined" style={{ fontSize: 18, color: PURPLE, flexShrink: 0 }}>psychology</span>
            <div style={{ minWidth: 0 }}>
              <div style={{ fontSize: 10, color: MUTED, fontWeight: 600, letterSpacing: 0.5, textTransform: 'uppercase', marginBottom: 1 }}>Model</div>
              <div style={{ fontSize: 12, color: TEXT, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontFamily: 'JetBrains Mono, monospace' }}>
                {o.llm_model ?? <span style={{ color: MUTED, fontStyle: 'italic' }}>env default</span>}
              </div>
            </div>
          </div>
          <div style={{ padding: '8px 12px', borderRadius: 10, background: 'var(--tm-filter-bg)', border: `1px solid ${BORDER}`, display: 'flex', alignItems: 'center', gap: 8 }}>
            <span className="material-symbols-outlined" style={{ fontSize: 18, color: CYAN, flexShrink: 0 }}>smart_toy</span>
            <div style={{ minWidth: 0 }}>
              <div style={{ fontSize: 10, color: MUTED, fontWeight: 600, letterSpacing: 0.5, textTransform: 'uppercase', marginBottom: 1 }}>Agents</div>
              <div style={{ fontSize: 12, color: TEXT, fontWeight: 600 }}>{agentCount} allowed</div>
            </div>
          </div>
        </div>

        {/* Limits row */}
        <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
          <span style={{ fontSize: 11, color: MUTED, padding: '3px 8px', borderRadius: 6, background: 'var(--tm-filter-bg)', border: `1px solid ${BORDER}` }}>⚡ {o.max_iterations} iters</span>
          <span style={{ fontSize: 11, color: MUTED, padding: '3px 8px', borderRadius: 6, background: 'var(--tm-filter-bg)', border: `1px solid ${BORDER}` }}>🔄 {o.rate_limit_rpm} rpm</span>
          {o.voice_enabled && (
            <span style={{ fontSize: 11, color: CYAN, padding: '3px 8px', borderRadius: 6, background: 'rgba(0,209,255,0.06)', border: '1px solid rgba(0,209,255,0.2)' }}>🎙 voice</span>
          )}
          {o.memory_enabled && (
            <span style={{ fontSize: 11, color: PURPLE, padding: '3px 8px', borderRadius: 6, background: 'rgba(167,139,250,0.06)', border: '1px solid rgba(167,139,250,0.2)' }}>🧠 memory</span>
          )}
          {o.llm_api_key_hint && (
            <span title={`Key: ${o.llm_api_key_hint}`} style={{ fontSize: 11, color: GREEN, padding: '3px 8px', borderRadius: 6, background: 'rgba(52,211,153,0.06)', border: '1px solid rgba(52,211,153,0.2)' }}>🔑 key set</span>
          )}
        </div>
      </div>

      {/* Action buttons */}
      <div style={{ borderTop: `1px solid ${BORDER}`, padding: '12px 16px', display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 8 }}>
        <button className="orch-card-btn orch-card-btn--test" onClick={() => router.push(`/admin/playground?orchestrator=${o.name}`)}>
          🧪 Test
        </button>
        <button className="orch-card-btn orch-card-btn--edit" onClick={() => onEdit(o)}>
          ✏️ Edit
        </button>
        {o.enabled ? (
          <button className="orch-card-btn orch-card-btn--toggle-on" onClick={async () => { await themApi.updateOrchestrator(o.id, { enabled: false }); onReload(); }}>
            🔴 Disable
          </button>
        ) : (
          <button className="orch-card-btn orch-card-btn--toggle-off" onClick={async () => { await themApi.updateOrchestrator(o.id, { enabled: true }); onReload(); }}>
            🟢 Enable
          </button>
        )}
      </div>
    </div>
  );
}
