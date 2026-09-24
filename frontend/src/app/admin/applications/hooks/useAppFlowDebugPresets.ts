'use client';
import { useCallback, useState } from 'react';
import { themApi, type AppFlowDebugPreset, type AppFlowLLMOverrideInput } from '@/lib/api';
import type { AppFlowRuntimeParamSpec, AppFlowLLMCredentialValue } from '../types';
import type { AppFlowDebugSessionState } from './useAppFlowDebugSession';

// useAppFlowDebugPresets — split out of useAppFlowDebugSession.ts (which was
// approaching the 400-line file-size guideline) to keep the debug-run
// WS/lifecycle logic and the preset save/load/delete logic in separate files.
// Presets are personal, per-(tenant, user, application), db/111 — save/reload
// the debug panel's entry point, test message, step mode, and per-node LLM
// overrides so a user doesn't have to re-type them every time the panel
// opens. See docs/UNIFIED_ROLE_GOVERNANCE_DESIGN.md for why these are
// deliberately NOT shared via the tenant Role model.
export function useAppFlowDebugPresets({
  appId,
  debug,
  setDebug,
  runtimeParamSpecs,
}: {
  appId: string;
  debug: AppFlowDebugSessionState;
  setDebug: (updater: (prev: AppFlowDebugSessionState) => AppFlowDebugSessionState) => void;
  runtimeParamSpecs: AppFlowRuntimeParamSpec[];
}) {
  const [presets, setPresets] = useState<AppFlowDebugPreset[]>([]);
  const [presetsLoaded, setPresetsLoaded] = useState(false);
  const [presetError, setPresetError] = useState<string | null>(null);

  // Loaded lazily on first openPanel(), not on mount, since the panel is
  // often never opened for a given app session.
  const loadPresets = useCallback(async () => {
    try {
      const list = await themApi.listAppFlowDebugPresets(appId);
      setPresets(list);
      setPresetsLoaded(true);
    } catch {
      // Non-fatal — presets are a convenience feature, not required to debug.
      setPresetsLoaded(true);
    }
  }, [appId]);

  // savePreset persists the CURRENT panel state (entry point, message, step
  // mode, credentials) under name, creating or overwriting a preset of that
  // name for this (tenant, user, application). Custom-mode api_key values are
  // sent once here and Fernet-encrypted server-side — they never come back
  // from List/Save, only a masked hint.
  const savePreset = useCallback(async (name: string) => {
    setPresetError(null);
    const llmOverrides: Record<string, AppFlowLLMOverrideInput> = {};
    for (const [specKey, val] of Object.entries(debug.credentials)) {
      const nodeId = specKey.split(':')[0];
      llmOverrides[nodeId] = val.mode === 'general'
        ? { mode: 'general', provider: val.provider, key_id: val.keyId, model: val.model || undefined }
        : { mode: 'custom', provider: val.provider, model: val.model, api_key: val.apiKey, base_url: val.baseUrl };
    }
    try {
      const saved = await themApi.saveAppFlowDebugPreset(appId, {
        name,
        entry_point_slug: debug.entryPointSlug,
        user_message: debug.userMessage,
        step_mode: debug.stepMode,
        llm_overrides: llmOverrides,
      });
      setPresets(prev => {
        const rest = prev.filter(p => p.name !== saved.name);
        return [...rest, saved].sort((a, b) => a.name.localeCompare(b.name));
      });
    } catch (e: unknown) {
      setPresetError(e instanceof Error ? e.message : 'Failed to save preset');
    }
  }, [appId, debug.entryPointSlug, debug.userMessage, debug.stepMode, debug.credentials]);

  // loadPreset applies a saved preset's entry point/message/step-mode onto
  // the panel. Custom-mode api_key fields come back masked, never plaintext —
  // the credential picker shows the stored provider/model but the user must
  // re-enter the API key before running (same "never persisted, never
  // returned" rule the debug-start endpoint itself already enforces).
  const loadPreset = useCallback((presetId: string) => {
    const preset = presets.find(p => p.id === presetId);
    if (!preset) return;
    const credentials: Record<string, AppFlowLLMCredentialValue> = {};
    for (const [nodeId, entry] of Object.entries(preset.llm_overrides)) {
      // specKey is `${nodeId}:${paramKey}` but the preset only stores nodeId —
      // runtimeParamSpecs resolves the paramKey per node at apply-time via the
      // matching specKey prefix, same lookup runAll already does in reverse.
      const spec = runtimeParamSpecs.find(s => s.nodeId === nodeId);
      const specKey = spec?.specKey ?? nodeId;
      credentials[specKey] = entry.mode === 'general'
        ? { mode: 'general', provider: entry.provider ?? '', keyId: entry.key_id ?? null, model: entry.model ?? '' }
        : { mode: 'custom', provider: entry.provider ?? '', model: entry.model ?? '', apiKey: '', baseUrl: entry.base_url ?? '' };
    }
    setDebug(prev => ({
      ...prev,
      entryPointSlug: preset.entry_point_slug,
      userMessage: preset.user_message,
      stepMode: preset.step_mode,
      credentials,
    }));
  }, [presets, runtimeParamSpecs, setDebug]);

  const deletePreset = useCallback(async (presetId: string) => {
    setPresetError(null);
    try {
      await themApi.deleteAppFlowDebugPreset(appId, presetId);
      setPresets(prev => prev.filter(p => p.id !== presetId));
    } catch (e: unknown) {
      setPresetError(e instanceof Error ? e.message : 'Failed to delete preset');
    }
  }, [appId]);

  return { presets, presetsLoaded, presetError, loadPresets, savePreset, loadPreset, deletePreset };
}
