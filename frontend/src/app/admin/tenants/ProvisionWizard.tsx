'use client';
import { useState } from 'react';
import { themApi, type TenantRecord, type TenantQuota, type QuotaPlan, type UserCreateInput } from '@/lib/api';

const ACCENT = '#818cf8';
const ACCENT_BORDER = 'rgba(129,140,248,0.4)';
const PLANS: QuotaPlan[] = ['trial', 'starter', 'pro', 'enterprise'];

const overlay: React.CSSProperties = {
  position: 'fixed', inset: 0, zIndex: 60, display: 'flex',
  alignItems: 'center', justifyContent: 'center', background: 'rgba(0,0,0,.55)',
};
const modal: React.CSSProperties = {
  background: 'var(--tm-sidebar)', borderRadius: '16px', padding: '28px',
  width: '460px', maxWidth: '96vw', border: '1px solid rgba(255,255,255,.1)',
  maxHeight: '90vh', overflowY: 'auto',
};
const inp: React.CSSProperties = {
  width: '100%', padding: '8px 12px', borderRadius: '8px', fontSize: '13px',
  background: 'var(--tm-inset)', border: '1px solid var(--tm-filter-border)',
  color: 'var(--tm-card-text)', outline: 'none', boxSizing: 'border-box',
};
const label: React.CSSProperties = {
  fontSize: '12px', fontWeight: 600, color: 'var(--tm-card-text-muted)',
  display: 'block', marginBottom: '5px',
};
const fieldWrap: React.CSSProperties = { marginBottom: '14px' };
const errStyle: React.CSSProperties = { fontSize: '12px', color: '#f87171', margin: '0 0 12px 0' };

// ── Step indicator ─────────────────────────────────────────────────────────────

function StepDots({ current, total }: { current: number; total: number }) {
  return (
    <div style={{ display: 'flex', gap: '6px', alignItems: 'center', marginBottom: '20px' }}>
      {Array.from({ length: total }).map((_, i) => (
        <div key={i} style={{
          width: i === current ? '20px' : '8px', height: '8px', borderRadius: '4px',
          background: i < current ? '#34d399' : i === current ? ACCENT : 'rgba(255,255,255,.15)',
          transition: 'all .2s',
        }} />
      ))}
    </div>
  );
}

// ── Step 1: Tenant ─────────────────────────────────────────────────────────────

function Step1Tenant({ onDone, onClose }: {
  onDone: (t: TenantRecord) => void;
  onClose: () => void;
}) {
  const [slug, setSlug] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState('');

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!slug || !displayName) { setErr('Both fields are required'); return; }
    setSaving(true); setErr('');
    try {
      const t = await themApi.createTenant({ slug, display_name: displayName });
      onDone(t);
    } catch (ex) { setErr((ex as Error).message || 'Error creating tenant'); }
    finally { setSaving(false); }
  }

  return (
    <form onSubmit={submit}>
      <StepDots current={0} total={4} />
      <h2 style={{ fontSize: '17px', fontWeight: 700, color: 'var(--tm-card-text)', margin: '0 0 6px 0' }}>New Tenant</h2>
      <p style={{ fontSize: '13px', color: 'var(--tm-card-text-muted)', margin: '0 0 20px 0' }}>Step 1 of 4 — Tenant identity</p>
      <div style={fieldWrap}>
        <label style={label}>Slug</label>
        <input value={slug} onChange={e => setSlug(e.target.value)} style={inp} placeholder="acme-corp" autoFocus />
        <p style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', margin: '4px 0 0 0' }}>
          Lowercase letters, numbers, hyphens, underscores (max 64 chars)
        </p>
      </div>
      <div style={fieldWrap}>
        <label style={label}>Display Name</label>
        <input value={displayName} onChange={e => setDisplayName(e.target.value)} style={inp} placeholder="Acme Corp" />
      </div>
      {err && <p style={errStyle}>{err}</p>}
      <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end' }}>
        <button type="button" onClick={onClose}
          style={{ padding: '8px 16px', borderRadius: '8px', fontSize: '13px', background: 'transparent', border: '1px solid rgba(255,255,255,.12)', color: 'var(--tm-card-text-muted)', cursor: 'pointer' }}>
          Cancel
        </button>
        <button type="submit" disabled={saving}
          style={{ padding: '8px 18px', borderRadius: '8px', fontSize: '13px', fontWeight: 600, background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT, cursor: saving ? 'not-allowed' : 'pointer', opacity: saving ? 0.6 : 1 }}>
          {saving ? 'Creating…' : 'Create Tenant →'}
        </button>
      </div>
    </form>
  );
}

// ── Step 2: Admin User ─────────────────────────────────────────────────────────

function Step2User({ tenant, onDone, onSkip }: {
  tenant: TenantRecord;
  onDone: () => void;
  onSkip: () => void;
}) {
  const suggested = `${tenant.slug}-admin`;
  const [username, setUsername] = useState(suggested);
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState('');

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!username || !name || !password) { setErr('Username, name, and password are required'); return; }
    setSaving(true); setErr('');
    const input: UserCreateInput = {
      username, name, password, role: 'viewer',
      tenant_id: tenant.id, tenant_role: 'admin',
    };
    if (email) input.email = email;
    try {
      await themApi.createUser(input);
      onDone();
    } catch (ex) {
      const msg = (ex as Error).message || '';
      setErr(msg.toLowerCase().includes('unique') || msg.toLowerCase().includes('duplicate') || msg.toLowerCase().includes('conflict') || msg.toLowerCase().includes('already')
        ? `Username "${username}" is already taken — choose a different one`
        : 'Error creating user');
    }
    finally { setSaving(false); }
  }

  return (
    <form onSubmit={submit}>
      <StepDots current={1} total={4} />
      <h2 style={{ fontSize: '17px', fontWeight: 700, color: 'var(--tm-card-text)', margin: '0 0 6px 0' }}>Tenant Admin User</h2>
      <p style={{ fontSize: '13px', color: 'var(--tm-card-text-muted)', margin: '0 0 20px 0' }}>
        Step 2 of 4 — Create an admin for <strong style={{ color: 'var(--tm-card-text)' }}>{tenant.display_name}</strong>
      </p>
      <div style={fieldWrap}>
        <label style={label}>Username</label>
        <input value={username} onChange={e => setUsername(e.target.value)} style={inp} placeholder={suggested} autoFocus />
        <p style={{ fontSize: '11px', color: 'var(--tm-card-text-muted)', margin: '4px 0 0 0' }}>
          Must be unique across the platform. Suggested: <code style={{ background: 'var(--tm-inset)', padding: '1px 4px', borderRadius: '3px' }}>{suggested}</code>
        </p>
      </div>
      <div style={fieldWrap}>
        <label style={label}>Display Name</label>
        <input value={name} onChange={e => setName(e.target.value)} style={inp} placeholder="Alice Smith" />
      </div>
      <div style={fieldWrap}>
        <label style={label}>Email (optional)</label>
        <input type="email" value={email} onChange={e => setEmail(e.target.value)} style={inp} placeholder="alice@acme.com" />
      </div>
      <div style={fieldWrap}>
        <label style={label}>Password</label>
        <input type="password" value={password} onChange={e => setPassword(e.target.value)} style={inp} />
      </div>
      {err && <p style={errStyle}>{err}</p>}
      <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end' }}>
        <button type="button" onClick={onSkip}
          style={{ padding: '8px 16px', borderRadius: '8px', fontSize: '13px', background: 'transparent', border: '1px solid rgba(255,255,255,.12)', color: 'var(--tm-card-text-muted)', cursor: 'pointer' }}>
          Skip
        </button>
        <button type="submit" disabled={saving}
          style={{ padding: '8px 18px', borderRadius: '8px', fontSize: '13px', fontWeight: 600, background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT, cursor: saving ? 'not-allowed' : 'pointer', opacity: saving ? 0.6 : 1 }}>
          {saving ? 'Creating…' : 'Create User →'}
        </button>
      </div>
    </form>
  );
}

// ── Step 3: Quota ──────────────────────────────────────────────────────────────

function Step3Quota({ tenant, onDone, onSkip }: {
  tenant: TenantRecord;
  onDone: () => void;
  onSkip: () => void;
}) {
  type QuotaForm = Omit<TenantQuota, 'tenant_id'>;
  const empty = (): QuotaForm => ({
    plan: 'trial', max_agents: null, max_apps: null, max_mcp_servers: null,
    max_concurrent_runs: null, max_users: null, monthly_llm_tokens: null,
    monthly_runs: null, api_requests_per_minute: null, runs_per_minute: null,
  });
  const [q, setQ] = useState<QuotaForm>(empty());
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState('');

  function numField(lbl: string, key: keyof Omit<QuotaForm, 'plan'>) {
    const val = q[key] as number | null;
    return (
      <div style={fieldWrap} key={key}>
        <label style={label}>{lbl} <span style={{ fontWeight: 400 }}>(blank = unlimited)</span></label>
        <input type="number" min={1} value={val ?? ''} style={inp}
          onChange={e => setQ(prev => ({ ...prev, [key]: e.target.value === '' ? null : parseInt(e.target.value, 10) }))} />
      </div>
    );
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setSaving(true); setErr('');
    try {
      await themApi.upsertTenantQuota(tenant.id, q);
      onDone();
    } catch (ex) { setErr((ex as Error).message || 'Error saving quota'); }
    finally { setSaving(false); }
  }

  return (
    <form onSubmit={submit}>
      <StepDots current={2} total={4} />
      <h2 style={{ fontSize: '17px', fontWeight: 700, color: 'var(--tm-card-text)', margin: '0 0 6px 0' }}>Quota</h2>
      <p style={{ fontSize: '13px', color: 'var(--tm-card-text-muted)', margin: '0 0 20px 0' }}>
        Step 3 of 4 — Set limits for <strong style={{ color: 'var(--tm-card-text)' }}>{tenant.display_name}</strong>
      </p>
      <div style={fieldWrap}>
        <label style={label}>Plan</label>
        <select value={q.plan} onChange={e => setQ(prev => ({ ...prev, plan: e.target.value as QuotaPlan }))} style={inp}>
          {PLANS.map(p => <option key={p} value={p}>{p}</option>)}
        </select>
      </div>
      {numField('Max Agents', 'max_agents')}
      {numField('Max Apps', 'max_apps')}
      {numField('Max MCP Servers', 'max_mcp_servers')}
      {numField('Max Users', 'max_users')}
      {numField('Max Concurrent Runs', 'max_concurrent_runs')}
      {numField('Runs / Minute', 'runs_per_minute')}
      {numField('Monthly Runs', 'monthly_runs')}
      {numField('API Requests / Minute', 'api_requests_per_minute')}
      {numField('Monthly LLM Tokens', 'monthly_llm_tokens')}
      {err && <p style={errStyle}>{err}</p>}
      <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end' }}>
        <button type="button" onClick={onSkip}
          style={{ padding: '8px 16px', borderRadius: '8px', fontSize: '13px', background: 'transparent', border: '1px solid rgba(255,255,255,.12)', color: 'var(--tm-card-text-muted)', cursor: 'pointer' }}>
          Skip
        </button>
        <button type="submit" disabled={saving}
          style={{ padding: '8px 18px', borderRadius: '8px', fontSize: '13px', fontWeight: 600, background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT, cursor: saving ? 'not-allowed' : 'pointer', opacity: saving ? 0.6 : 1 }}>
          {saving ? 'Saving…' : 'Save Quota →'}
        </button>
      </div>
    </form>
  );
}

// ── Step 4: Done ───────────────────────────────────────────────────────────────

function Step4Done({ tenant, skipped, onClose }: {
  tenant: TenantRecord;
  skipped: { user: boolean; quota: boolean };
  onClose: () => void;
}) {
  return (
    <div>
      <StepDots current={3} total={4} />
      <div style={{ textAlign: 'center', padding: '16px 0 24px' }}>
        <span className="material-symbols-outlined" style={{ fontSize: '48px', color: '#34d399', display: 'block', marginBottom: '12px' }}>
          check_circle
        </span>
        <h2 style={{ fontSize: '17px', fontWeight: 700, color: 'var(--tm-card-text)', margin: '0 0 6px 0' }}>
          {tenant.display_name} is ready
        </h2>
        <p style={{ fontSize: '13px', color: 'var(--tm-card-text-muted)', margin: '0 0 20px 0' }}>
          Tenant <code style={{ background: 'var(--tm-inset)', padding: '1px 6px', borderRadius: '4px' }}>{tenant.slug}</code> created
        </p>
        {(skipped.user || skipped.quota) && (
          <div style={{ background: 'rgba(245,158,11,.08)', border: '1px solid rgba(245,158,11,.2)', borderRadius: '8px', padding: '10px 14px', marginBottom: '20px', textAlign: 'left' }}>
            <p style={{ fontSize: '12px', color: '#f59e0b', margin: 0, fontWeight: 600 }}>Skipped:</p>
            {skipped.user && <p style={{ fontSize: '12px', color: '#f59e0b', margin: '4px 0 0 0' }}>• No admin user created — add one later in the Tenants panel</p>}
            {skipped.quota && <p style={{ fontSize: '12px', color: '#f59e0b', margin: '4px 0 0 0' }}>• No quota set — tenant runs with no limits</p>}
          </div>
        )}
      </div>
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <button onClick={onClose}
          style={{ padding: '8px 18px', borderRadius: '8px', fontSize: '13px', fontWeight: 600, background: `${ACCENT}22`, border: `1px solid ${ACCENT_BORDER}`, color: ACCENT, cursor: 'pointer' }}>
          Done
        </button>
      </div>
    </div>
  );
}

// ── Wizard shell ───────────────────────────────────────────────────────────────

export default function ProvisionWizard({ onClose, onCreated }: {
  onClose: () => void;
  onCreated: (t: TenantRecord) => void;
}) {
  const [step, setStep] = useState<1 | 2 | 3 | 4>(1);
  const [tenant, setTenant] = useState<TenantRecord | null>(null);
  const [skipped, setSkipped] = useState({ user: false, quota: false });

  function handleTenantCreated(t: TenantRecord) {
    setTenant(t);
    setStep(2);
  }

  function handleUserDone() { setStep(3); }
  function handleUserSkip() { setSkipped(s => ({ ...s, user: true })); setStep(3); }

  function handleQuotaDone() { setStep(4); }
  function handleQuotaSkip() { setSkipped(s => ({ ...s, quota: true })); setStep(4); }

  function handleDone() {
    if (tenant) onCreated(tenant);
    onClose();
  }

  return (
    <div style={overlay} onClick={onClose}>
      <div style={modal} onClick={e => e.stopPropagation()}>
        {step === 1 && <Step1Tenant onDone={handleTenantCreated} onClose={onClose} />}
        {step === 2 && tenant && <Step2User tenant={tenant} onDone={handleUserDone} onSkip={handleUserSkip} />}
        {step === 3 && tenant && <Step3Quota tenant={tenant} onDone={handleQuotaDone} onSkip={handleQuotaSkip} />}
        {step === 4 && tenant && <Step4Done tenant={tenant} skipped={skipped} onClose={handleDone} />}
      </div>
    </div>
  );
}
