export type Scenario = {
  key: string;
  label: string;
  accessMode: string;
  allowedPrincipals: string;
  prerequisite: string;
  requiresRuntimeIdp: boolean;
};

export const SCENARIOS: Scenario[] = [
  {
    key: 'staff',
    label: 'the-M user account',
    accessMode: 'user_jwt',
    allowedPrincipals: 'internal',
    requiresRuntimeIdp: false,
    prerequisite: 'Users must have a the-M account on this tenant',
  },
  {
    key: 'service_token',
    label: 'Service token',
    accessMode: 'token',
    allowedPrincipals: 'internal',
    requiresRuntimeIdp: false,
    prerequisite: 'An access token must be created in Admin → Tokens',
  },
  {
    key: 'backend_service',
    label: 'Backend service (on behalf of user)',
    accessMode: 'token',
    allowedPrincipals: 'external',
    requiresRuntimeIdp: false,
    prerequisite: 'A token with is_backend=true must be provisioned',
  },
  {
    key: 'org_jwt',
    label: 'Organization-issued access token',
    accessMode: 'external_jwt',
    allowedPrincipals: 'external',
    requiresRuntimeIdp: true,
    prerequisite: 'Runtime Identity must be configured in Tenant Settings → Runtime Identity',
  },
  {
    key: 'service_token_any',
    label: 'Service token — regular or backend',
    accessMode: 'token',
    allowedPrincipals: 'both',
    requiresRuntimeIdp: false,
    prerequisite: 'An access token must be created in Admin → Tokens',
  },
  {
    key: 'open',
    label: 'Open (no auth)',
    accessMode: 'public',
    allowedPrincipals: 'both',
    requiresRuntimeIdp: false,
    prerequisite: 'None — any caller is admitted without credentials',
  },
];

// Dead combinations — these (access_mode, allowed_principals) pairs reject all callers.
const DEAD: Array<[string, string]> = [
  ['external_jwt', 'internal'],
  ['user_jwt', 'external'],
  ['public', 'external'],
];

export function isDead(accessMode: string, allowedPrincipals: string): boolean {
  return DEAD.some(([m, p]) => m === accessMode && p === allowedPrincipals);
}

// Maps a (access_mode, allowed_principals) pair to the nearest scenario key.
// Returns null for genuinely dead combinations, undefined for unknown valid combinations.
export function resolveScenario(accessMode: string, allowedPrincipals: string): string | null | undefined {
  if (isDead(accessMode, allowedPrincipals)) return null;

  // Exact match first.
  const exact = SCENARIOS.find(s => s.accessMode === accessMode && s.allowedPrincipals === allowedPrincipals);
  if (exact) return exact.key;

  // Legacy equivalents — valid but not in the canonical list; display nearest without rewriting.
  if (accessMode === 'user_jwt'     && allowedPrincipals === 'both')     return 'staff';
  if (accessMode === 'external_jwt' && allowedPrincipals === 'both')     return 'org_jwt';
  if (accessMode === 'public'       && allowedPrincipals === 'internal') return 'open';

  return undefined; // unknown combination
}
