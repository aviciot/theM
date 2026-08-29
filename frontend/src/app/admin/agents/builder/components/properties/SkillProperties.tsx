import type { Node } from '@xyflow/react';
import type { SkillData } from '../../types';
import { C, labelStyle, inputStyle, textareaStyle, fieldGap, hint } from '../../constants';

interface SkillPropertiesProps {
  selectedNode: Node;
  agentNodes: Node[];
  activeView: 'agent' | 'skill';
  setAgentNodes: (updater: (prev: Node[]) => Node[]) => void;
  setDirty: (dirty: boolean) => void;
  savePipelineState: () => void;
  setActiveSkillId: (id: string | null) => void;
  setActiveView: (view: 'agent' | 'skill') => void;
  setSelectedNode: (node: Node | null) => void;
  updateSelectedNodeField: (field: string, value: string) => void;
}

export function SkillProperties({
  selectedNode, agentNodes, activeView, setAgentNodes, setDirty,
  savePipelineState, setActiveSkillId, setActiveView, setSelectedNode,
  updateSelectedNodeField,
}: SkillPropertiesProps) {
  const liveSkillNode = agentNodes.find(n => n.id === selectedNode.id);
  const d = (liveSkillNode?.data ?? selectedNode.data) as unknown as SkillData;
  const skillNodeId = selectedNode.id;
  const MODES = ['text/plain', 'text/markdown', 'application/json', 'application/octet-stream'];

  function updateSkillArray(field: keyof SkillData, arr: string[]) {
    if (activeView === 'agent') {
      setAgentNodes(prev => prev.map(n =>
        n.id === skillNodeId ? { ...n, data: { ...n.data, [field]: arr } } : n
      ));
    }
    setDirty(true);
  }

  function toggleMode(field: 'input_modes' | 'output_modes', mode: string) {
    const current = (d[field] ?? []) as string[];
    const next = current.includes(mode) ? current.filter(m => m !== mode) : [...current, mode];
    updateSkillArray(field, next.length ? next : [mode]);
  }

  return (
    <>
      <label style={labelStyle}>Skill ID <span style={{ fontWeight: 400, color: '#475569' }}>(auto-generated)</span></label>
      <div style={{ ...inputStyle, fontFamily: 'JetBrains Mono, monospace', fontSize: '10px', color: '#475569', userSelect: 'all', cursor: 'text', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
        {d.skill_id}
      </div>
      <div style={fieldGap}>
        <label style={labelStyle}>Name</label>
        <input value={d.name} onChange={e => updateSelectedNodeField('name', e.target.value)} style={inputStyle} />
      </div>
      <div style={fieldGap}>
        <label style={labelStyle}>Description</label>
        <textarea value={d.description} onChange={e => updateSelectedNodeField('description', e.target.value)} rows={2} style={textareaStyle} />
      </div>
      <div style={fieldGap}>
        <label style={labelStyle}>Input Modes</label>
        {MODES.map(m => (
          <label key={m} style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4, cursor: 'pointer', fontSize: '12px', color: '#ccc' }}>
            <input type="checkbox" checked={(d.input_modes ?? []).includes(m)} onChange={() => toggleMode('input_modes', m)} style={{ accentColor: C.cyan }} />
            {m}
          </label>
        ))}
      </div>
      <div style={fieldGap}>
        <label style={labelStyle}>Output Modes</label>
        {MODES.map(m => (
          <label key={m} style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4, cursor: 'pointer', fontSize: '12px', color: '#ccc' }}>
            <input type="checkbox" checked={(d.output_modes ?? []).includes(m)} onChange={() => toggleMode('output_modes', m)} style={{ accentColor: C.purple }} />
            {m}
          </label>
        ))}
      </div>
      <div style={fieldGap}>
        <label style={labelStyle}>Tags <span style={hint}>comma-separated</span></label>
        <input
          value={(d.tags ?? []).join(', ')}
          onChange={e => updateSkillArray('tags', e.target.value.split(',').map(t => t.trim()).filter(Boolean))}
          style={inputStyle}
          placeholder="search, nlp, ..."
        />
      </div>
      <div style={fieldGap}>
        <label style={labelStyle}>Examples</label>
        {(d.examples ?? []).map((ex, i) => (
          <div key={i} style={{ display: 'flex', gap: 4, marginBottom: 4 }}>
            <input
              value={ex}
              onChange={e => {
                const next = [...(d.examples ?? [])];
                next[i] = e.target.value;
                updateSkillArray('examples', next);
              }}
              style={{ ...inputStyle, flex: 1, fontSize: '12px' }}
              placeholder="e.g. Summarize this article"
            />
            <button
              onClick={() => updateSkillArray('examples', (d.examples ?? []).filter((_, j) => j !== i))}
              style={{ background: 'transparent', border: 'none', color: '#f87171', cursor: 'pointer', fontSize: '14px', padding: '0 4px' }}
            >×</button>
          </div>
        ))}
        <button
          onClick={() => updateSkillArray('examples', [...(d.examples ?? []), ''])}
          style={{ marginTop: 4, background: 'transparent', border: `1px dashed ${C.outline}`, color: C.textMuted, padding: '4px 10px', borderRadius: '4px', cursor: 'pointer', fontSize: '11px', width: '100%' }}
        >+ Add example</button>
      </div>
      <button onClick={() => {
        savePipelineState();
        setActiveSkillId(d.skill_id);
        setActiveView('skill');
        setSelectedNode(null);
      }} style={{
        marginTop: '16px', width: '100%', background: C.purpleBg,
        border: `1px solid ${C.purpleBorder}`, color: C.purple,
        padding: '8px', borderRadius: '6px', cursor: 'pointer', fontSize: '12px', fontWeight: 600,
      }}>
        Edit Pipeline
      </button>
    </>
  );
}
