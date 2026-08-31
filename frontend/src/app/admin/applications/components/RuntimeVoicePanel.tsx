'use client';
import { C, STT_PROVIDERS, STT_MODELS, TTS_PROVIDERS, TTS_VOICES_OPENAI, TTS_MODELS_OPENAI } from '../constants';
import { type SaveBtn, sharedField, sharedLbl, ToggleBtn } from './RuntimeShared';

export type VoiceDraft = {
  stt_provider: string; stt_model: string;
  tts_provider: string; tts_voice: string; tts_model: string;
  voice_enabled: boolean; tts_enabled: boolean;
};

export function VoicePanel({ orchId, vd, setVd, vBusy, vTest, tTest, vMsg, vtMsg, ttMsg, onSave, onTestSTT, onTestTTS, saveBtn }: {
  orchId: string;
  vd: VoiceDraft;
  setVd: (patch: Partial<VoiceDraft>) => void;
  vBusy: boolean; vTest: boolean; tTest: boolean;
  vMsg: string; vtMsg: string; ttMsg: string;
  onSave: () => void; onTestSTT: () => void; onTestTTS: () => void;
  saveBtn: SaveBtn;
}) {
  void orchId;
  const f = sharedField;
  const l = sharedLbl;
  return (
    <div style={{ padding: '12px 12px', display: 'flex', flexDirection: 'column', gap: 14 }}>
      {/* STT */}
      <div>
        <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.07em', marginBottom: 7, display: 'flex', alignItems: 'center', gap: 5 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 13 }}>mic</span>
          Speech-to-Text (STT)
          <span style={{ marginLeft: 'auto' }}>
            <ToggleBtn on={vd.voice_enabled} onToggle={() => setVd({ voice_enabled: !vd.voice_enabled })} title={vd.voice_enabled ? 'Disable STT' : 'Enable STT'} />
          </span>
        </div>
        <div style={{ opacity: vd.voice_enabled ? 1 : 0.45, display: 'flex', gap: 8, alignItems: 'center' }}>
          <select value={vd.stt_provider} disabled={!vd.voice_enabled}
            onChange={e => { const p = e.target.value; setVd({ stt_provider: p, stt_model: (STT_MODELS[p] ?? [])[0] ?? '' }); }}
            style={{ ...f, width: 140, flexShrink: 0 }}>
            <option value="">— provider —</option>
            {(STT_PROVIDERS as readonly string[]).map(p => <option key={p} value={p}>{p}</option>)}
          </select>
          <select value={vd.stt_model} disabled={!vd.voice_enabled || !vd.stt_provider}
            onChange={e => setVd({ stt_model: e.target.value })}
            style={{ ...f, flex: 1, fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}>
            <option value="">— model —</option>
            {(STT_MODELS[vd.stt_provider] ?? []).map(m => <option key={m} value={m}>{m}</option>)}
          </select>
          <button onClick={onTestSTT} disabled={vBusy || vTest || !vd.stt_provider}
            style={{ padding: '8px 12px', borderRadius: 7, border: '1px solid rgba(74,222,128,0.3)', background: 'rgba(74,222,128,0.07)', color: C.green, cursor: 'pointer', fontSize: 12, fontWeight: 600, opacity: (vBusy || vTest || !vd.stt_provider) ? 0.45 : 1, flexShrink: 0 }}>
            {vTest ? '…' : 'Test'}
          </button>
        </div>
        {vtMsg && <div style={{ marginTop: 5, fontSize: 12, color: vtMsg.startsWith('✓') ? C.green : C.error, fontWeight: 600 }}>{vtMsg}</div>}
      </div>

      {/* TTS */}
      <div style={{ borderTop: '1px solid rgba(132,158,190,0.1)', paddingTop: 12 }}>
        <div style={{ fontSize: 11, fontWeight: 700, color: C.textMuted, textTransform: 'uppercase', letterSpacing: '0.07em', marginBottom: 7, display: 'flex', alignItems: 'center', gap: 5 }}>
          <span className="material-symbols-outlined" style={{ fontSize: 13 }}>volume_up</span>
          Text-to-Speech (TTS)
          <span style={{ marginLeft: 'auto' }}>
            <ToggleBtn on={vd.tts_enabled} onToggle={() => setVd({ tts_enabled: !vd.tts_enabled })} title={vd.tts_enabled ? 'Disable TTS' : 'Enable TTS'} />
          </span>
        </div>
        <div style={{ opacity: vd.tts_enabled ? 1 : 0.45, display: 'flex', flexDirection: 'column', gap: 8 }}>
          <div style={{ display: 'flex', gap: 8 }}>
            <select value={vd.tts_provider} disabled={!vd.tts_enabled}
              onChange={e => { const p = e.target.value; setVd({ tts_provider: p, tts_voice: p === 'openai' ? 'alloy' : '', tts_model: p === 'openai' ? 'tts-1' : '' }); }}
              style={{ ...f, width: 140, flexShrink: 0 }}>
              <option value="">— provider —</option>
              {(TTS_PROVIDERS as readonly string[]).map(p => <option key={p} value={p}>{p}</option>)}
            </select>
            {vd.tts_provider === 'openai' && (
              <select value={vd.tts_model} disabled={!vd.tts_enabled}
                onChange={e => setVd({ tts_model: e.target.value })}
                style={{ ...f, width: 130, flexShrink: 0, fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}>
                {(TTS_MODELS_OPENAI as readonly string[]).map(m => <option key={m} value={m}>{m}</option>)}
              </select>
            )}
          </div>
          {vd.tts_provider === 'openai' && (
            <div>
              <label style={l}>Voice</label>
              <select value={vd.tts_voice} disabled={!vd.tts_enabled}
                onChange={e => setVd({ tts_voice: e.target.value })}
                style={{ ...f, fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }}>
                <option value="">— voice —</option>
                {(TTS_VOICES_OPENAI as readonly string[]).map(v => <option key={v} value={v}>{v}</option>)}
              </select>
            </div>
          )}
          {vd.tts_provider === 'elevenlabs' && (
            <div>
              <label style={l}>Voice ID (from ElevenLabs dashboard)</label>
              <input type="text" placeholder="e.g. 21m00Tcm4TlvDq8ikWAM" value={vd.tts_voice} disabled={!vd.tts_enabled}
                onChange={e => setVd({ tts_voice: e.target.value })}
                style={{ ...f, fontFamily: 'JetBrains Mono, monospace', fontSize: 12 }} />
            </div>
          )}
          <button onClick={onTestTTS} disabled={vBusy || tTest || !vd.tts_provider || !vd.tts_voice}
            style={{ alignSelf: 'flex-start', padding: '8px 12px', borderRadius: 7, border: '1px solid rgba(74,222,128,0.3)', background: 'rgba(74,222,128,0.07)', color: C.green, cursor: 'pointer', fontSize: 12, fontWeight: 600, opacity: (vBusy || tTest || !vd.tts_provider || !vd.tts_voice) ? 0.45 : 1 }}>
            {tTest ? '…' : 'Test TTS'}
          </button>
          {ttMsg && <div style={{ fontSize: 12, color: ttMsg.startsWith('✓') ? C.green : C.error, fontWeight: 600 }}>{ttMsg}</div>}
        </div>
      </div>

      <div style={{ borderTop: '1px solid rgba(132,158,190,0.1)', paddingTop: 12, display: 'flex', gap: 8, alignItems: 'center' }}>
        {saveBtn(onSave, vBusy, false, 'Save Voice Config')}
        {vMsg && <span style={{ fontSize: 12, color: vMsg !== 'Saved' ? C.error : C.green, fontWeight: 600 }}>{vMsg}</span>}
      </div>
    </div>
  );
}
