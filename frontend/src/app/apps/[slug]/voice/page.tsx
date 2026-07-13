'use client';

import { useEffect, useState, use } from 'react';
import {
  LiveKitRoom,
  VoiceAssistantControlBar,
  useVoiceAssistant,
  BarVisualizer,
  RoomAudioRenderer,
  useChat,
} from '@livekit/components-react';
import '@livekit/components-styles';

// ── Types ────────────────────────────────────────────────────────────────────
interface TokenResponse {
  token: string;
  url: string;
  room: string;
  context_id: string;
}

// ── Constants ─────────────────────────────────────────────────────────────────
const CONTEXT_KEY = (slug: string) => `them:voice:context:${slug}`;

// ── Inner voice room (rendered inside LiveKitRoom context) ────────────────────
function VoiceRoomInner({ slug }: { slug: string }) {
  const { state: agentState, audioTrack } = useVoiceAssistant();
  const { chatMessages } = useChat();

  const isListening  = agentState === 'listening';
  const isSpeaking   = agentState === 'speaking';
  const isThinking   = agentState === 'thinking';
  const isConnecting = agentState === 'connecting';

  const stateLabel =
    isConnecting ? 'Connecting…'
    : isListening ? 'Listening'
    : isThinking  ? 'Thinking…'
    : isSpeaking  ? 'Speaking'
    : 'Ready';

  const stateColor =
    isConnecting ? '#64748b'
    : isListening ? '#00d1ff'
    : isThinking  ? '#a78bfa'
    : isSpeaking  ? '#4ade80'
    : '#94a3b8';

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', minHeight: '100dvh', gap: 0 }}>
      <RoomAudioRenderer />

      {/* Center section — visualizer + state indicator */}
      <div style={{
        flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
        gap: 32, padding: '0 24px',
      }}>
        {/* Agent visualizer orb */}
        <div style={{
          position: 'relative', width: 180, height: 180,
          display: 'flex', alignItems: 'center', justifyContent: 'center',
        }}>
          {/* Glow ring */}
          <div style={{
            position: 'absolute', inset: -16,
            borderRadius: '50%',
            background: `radial-gradient(circle, ${stateColor}22 0%, transparent 70%)`,
            transition: 'background 0.4s ease',
            pointerEvents: 'none',
          }} />
          {/* Visualizer container */}
          <div style={{
            width: 160, height: 160, borderRadius: '50%',
            background: 'var(--tm-card, rgba(255,255,255,0.04))',
            border: `2px solid ${stateColor}44`,
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            overflow: 'hidden',
            boxShadow: `0 0 40px ${stateColor}22, inset 0 0 24px rgba(0,0,0,0.3)`,
            transition: 'border-color 0.4s ease, box-shadow 0.4s ease',
          }}>
            {audioTrack ? (
              <BarVisualizer
                state={agentState}
                trackRef={audioTrack}
                barCount={24}
                style={{ width: '100%', height: '100%' }}
                options={{ minHeight: 2 }}
              />
            ) : (
              /* Static placeholder rings when no audio track */
              <div style={{
                width: 64, height: 64, borderRadius: '50%',
                border: `2px solid ${stateColor}55`,
                display: 'flex', alignItems: 'center', justifyContent: 'center',
              }}>
                <div style={{ width: 32, height: 32, borderRadius: '50%', background: `${stateColor}33` }} />
              </div>
            )}
          </div>
        </div>

        {/* State label */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 8,
          padding: '6px 16px', borderRadius: 20,
          background: `${stateColor}14`,
          border: `1px solid ${stateColor}33`,
          transition: 'all 0.3s ease',
        }}>
          <span style={{
            width: 7, height: 7, borderRadius: '50%',
            background: stateColor,
            boxShadow: (isListening || isSpeaking) ? `0 0 8px ${stateColor}` : 'none',
            display: 'inline-block',
            transition: 'box-shadow 0.3s ease',
          }} />
          <span style={{ fontSize: 13, fontWeight: 600, color: stateColor, letterSpacing: 0.3 }}>
            {stateLabel}
          </span>
        </div>
      </div>

      {/* Transcript */}
      {chatMessages.length > 0 && (
        <div style={{
          maxHeight: '28vh', overflowY: 'auto', padding: '12px 20px',
          borderTop: '1px solid rgba(255,255,255,0.07)',
          display: 'flex', flexDirection: 'column', gap: 8,
          scrollbarWidth: 'thin',
          scrollbarColor: 'rgba(0,209,255,0.2) transparent',
        }}>
          {chatMessages.slice(-20).map((msg, i) => {
            const isAgent = msg.from?.identity !== 'user';
            return (
              <div key={i} style={{
                display: 'flex', justifyContent: isAgent ? 'flex-start' : 'flex-end',
              }}>
                <div style={{
                  maxWidth: '80%', padding: '8px 12px',
                  borderRadius: isAgent ? '4px 12px 12px 12px' : '12px 4px 12px 12px',
                  background: isAgent ? 'rgba(0,209,255,0.07)' : 'rgba(255,255,255,0.06)',
                  border: `1px solid ${isAgent ? 'rgba(0,209,255,0.18)' : 'rgba(255,255,255,0.1)'}`,
                  fontSize: 13, color: 'var(--tm-card-text, #e2e8f0)',
                  lineHeight: 1.55,
                }}>
                  {msg.message}
                </div>
              </div>
            );
          })}
        </div>
      )}

      {/* Control bar */}
      <div style={{
        padding: '16px 24px 28px', display: 'flex', justifyContent: 'center',
        borderTop: chatMessages.length > 0 ? 'none' : '1px solid rgba(255,255,255,0.07)',
      }}>
        <VoiceAssistantControlBar />
      </div>
    </div>
  );
}

// ── Page ─────────────────────────────────────────────────────────────────────
export default function VoicePage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = use(params);
  const [tokenData, setTokenData] = useState<TokenResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!slug) return;

    async function fetchToken() {
      try {
        // Restore context_id from localStorage for conversation continuity
        const savedContextId = localStorage.getItem(CONTEXT_KEY(slug));
        const tokenUrl = savedContextId
          ? `/api/apps/${slug}/webrtc/token?context_id=${encodeURIComponent(savedContextId)}`
          : `/api/apps/${slug}/webrtc/token`;

        const res = await fetch(tokenUrl);

        if (res.status === 404) {
          setError('Application not found or WebRTC is not enabled for this app.');
          return;
        }
        if (res.status === 400) {
          const body = await res.json().catch(() => ({}));
          setError((body as any)?.detail ?? 'This application does not support voice.');
          return;
        }
        if (!res.ok) {
          const body = await res.json().catch(() => ({}));
          setError((body as any)?.detail ?? `Error ${res.status} — please try again.`);
          return;
        }

        const data: TokenResponse = await res.json();
        // Persist context_id for continuity across reconnects
        if (data.context_id) {
          localStorage.setItem(CONTEXT_KEY(slug), data.context_id);
        }
        setTokenData(data);
      } catch {
        setError('Could not connect to the server. Please check your network and try again.');
      } finally {
        setLoading(false);
      }
    }

    fetchToken();
  }, [slug]);

  // ── Shared page shell ──────────────────────────────────────────────────────
  const shell = (children: React.ReactNode) => (
    <div style={{
      minHeight: '100dvh', background: 'var(--tm-bg, #030d1a)',
      display: 'flex', flexDirection: 'column', fontFamily: 'Inter, sans-serif',
      color: 'var(--tm-card-text, #e2e8f0)',
    }}>
      {/* Header */}
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '14px 20px', flexShrink: 0,
        borderBottom: '1px solid rgba(255,255,255,0.06)',
        background: 'rgba(0,0,0,0.2)', backdropFilter: 'blur(12px)',
      }}>
        {/* Logo mark */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <svg width="18" height="14" viewBox="0 0 1407 1118" style={{ opacity: 0.8, flexShrink: 0 }}>
            <polygon points="88,77 184,146 244,191 281,217 336,259 355,272 358,272 367,267 372,266 379,262 391,258 433,239 440,237 473,222 513,206 520,202 546,192 555,187 558,187 446,102 433,91 421,83 403,68 397,65 392,60 331,15 318,4 274,19 264,21 246,28 239,29 217,37 214,37 211,39 201,41 189,46 186,46 154,57 151,57 148,59 141,60 138,62 104,73 101,73 98,75" fill="#a0f0d0"/>
            <polygon points="1323,77 1313,75 1292,67 1289,67 1239,50 1236,50 1233,48 1230,48 1189,34 1176,31 1094,4 1085,12 1074,19 1053,36 959,106 855,187 876,196 881,197 973,237 980,239 1034,263 1048,268 1055,272 1059,272 1139,213 1146,209 1177,185 1188,178 1208,162 1284,107" fill="#a0f0d0"/>
            <polygon points="70,97 70,334 71,335 72,350 76,365 104,429 108,435 180,486 184,490 245,534 345,609 339,293 305,269 281,250 252,230 182,177 179,176 153,156 150,155" fill="#a0f0d0"/>
            <polygon points="1342,97 1252,162 1248,166 1152,236 1148,240 1126,255 1122,259 1112,265 1103,273 1074,293 1073,296 1073,317 1072,318 1072,355 1071,356 1071,415 1070,416 1070,461 1069,462 1069,526 1068,527 1067,609 1306,433 1325,392 1336,365 1341,343 1341,331 1342,330" fill="#a0f0d0"/>
          </svg>
          <span style={{ fontSize: 14, fontWeight: 700, color: '#a0f0d0', letterSpacing: 0.5 }}>the-M</span>
        </div>
        {/* App slug */}
        <span style={{ fontSize: 11, color: 'rgba(255,255,255,0.3)', fontFamily: 'JetBrains Mono, monospace' }}>
          {slug}
        </span>
        {/* Voice badge */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 6,
          padding: '4px 10px', borderRadius: 20,
          background: 'rgba(167,139,250,0.1)', border: '1px solid rgba(167,139,250,0.25)',
        }}>
          <span style={{ fontSize: 13, color: '#a78bfa' }}>&#127908;</span>
          <span style={{ fontSize: 11, fontWeight: 600, color: '#a78bfa' }}>Voice</span>
        </div>
      </div>

      {/* Content */}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
        {children}
      </div>
    </div>
  );

  // ── Loading ────────────────────────────────────────────────────────────────
  if (loading) {
    return shell(
      <div style={{
        flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
        gap: 20, padding: 40,
      }}>
        <style>{`
          @keyframes voice-pulse {
            0%, 100% { box-shadow: 0 0 0 0 rgba(167,139,250,0.4); }
            50% { box-shadow: 0 0 0 14px rgba(167,139,250,0); }
          }
        `}</style>
        <div style={{
          width: 80, height: 80, borderRadius: '50%',
          background: 'radial-gradient(circle, rgba(167,139,250,0.2) 0%, transparent 70%)',
          border: '2px solid rgba(167,139,250,0.3)',
          animation: 'voice-pulse 1.5s ease-in-out infinite',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          fontSize: 28,
        }}>
          &#127908;
        </div>
        <span style={{ fontSize: 14, color: 'rgba(255,255,255,0.4)', fontWeight: 500 }}>
          Connecting to voice session…
        </span>
      </div>
    );
  }

  // ── Error ──────────────────────────────────────────────────────────────────
  if (error || !tokenData) {
    return shell(
      <div style={{
        flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
        gap: 16, padding: 40,
      }}>
        <div style={{
          width: 64, height: 64, borderRadius: '50%',
          background: 'rgba(255,180,171,0.1)', border: '2px solid rgba(255,180,171,0.3)',
          display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 28,
        }}>
          &#9888;
        </div>
        <div style={{ textAlign: 'center', maxWidth: 380 }}>
          <div style={{ fontSize: 16, fontWeight: 700, color: '#ffb4ab', marginBottom: 8 }}>
            Unable to start voice session
          </div>
          <div style={{ fontSize: 13, color: 'rgba(255,255,255,0.4)', lineHeight: 1.6 }}>
            {error ?? 'Something went wrong. Please try refreshing the page.'}
          </div>
        </div>
        <button
          onClick={() => window.location.reload()}
          style={{
            marginTop: 8, padding: '8px 20px', borderRadius: 8,
            background: 'rgba(255,180,171,0.12)', border: '1px solid rgba(255,180,171,0.3)',
            color: '#ffb4ab', fontSize: 13, fontWeight: 600, cursor: 'pointer',
          }}
        >
          Try again
        </button>
      </div>
    );
  }

  // ── Voice room ─────────────────────────────────────────────────────────────
  return shell(
    <LiveKitRoom
      serverUrl={tokenData.url}
      token={tokenData.token}
      connect={true}
      audio={true}
      video={false}
      style={{ flex: 1, display: 'flex', flexDirection: 'column', background: 'transparent' }}
    >
      <VoiceRoomInner slug={slug} />
    </LiveKitRoom>
  );
}
