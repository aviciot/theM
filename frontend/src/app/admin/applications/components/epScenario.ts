export type Scenario = {
  key: string;
  label: string;
  description: string;
  whenToUse: string;
  accessMode: string;
  allowedPrincipals: string;
  prerequisite: string;
  requiresRuntimeIdp: boolean;
};

export const SCENARIOS: Scenario[] = [
  {
    key: 'staff',
    label: 'Staff login',
    description: 'A person who has a the-M account logs in and connects. Their login session is the credential.',
    whenToUse: 'Your own team testing the app, or internal staff using it directly.',
    accessMode: 'user_jwt',
    allowedPrincipals: 'internal',
    requiresRuntimeIdp: false,
    prerequisite: 'Users must have a the-M account on this tenant',
  },
  {
    key: 'service_token',
    label: 'API key — no user',
    description: 'A machine connects using a long-lived API key. No user identity is attached — the platform only knows the key, not who is behind the call.',
    whenToUse: 'Automated scripts, test pipelines, batch jobs — any programmatic access where there is no real end user.',
    accessMode: 'token',
    allowedPrincipals: 'internal',
    requiresRuntimeIdp: false,
    prerequisite: 'Create an Internal token in Admin → Tokens',
  },
  {
    key: 'backend_service',
    label: 'API key — with user identity',
    description: 'A machine connects using an API key, and also tells the platform who the end user is. The platform records each user\'s sessions separately.',
    whenToUse: 'Your server calls the app on behalf of real customers. You want per-user conversation history and session tracking.',
    accessMode: 'token',
    allowedPrincipals: 'external',
    requiresRuntimeIdp: false,
    prerequisite: 'Create a Service token in Admin → Tokens',
  },
  {
    key: 'org_jwt',
    label: 'External identity token',
    description: 'End users connect directly using a token issued by your own identity system (SSO, Okta, Auth0, Keycloak). The platform validates the token without issuing one itself.',
    whenToUse: 'Your customers already log in via your SSO and you want them to connect directly — no server in the middle.',
    accessMode: 'external_jwt',
    allowedPrincipals: 'external',
    requiresRuntimeIdp: true,
    prerequisite: 'Runtime Identity must be configured in Tenant Settings → Runtime Identity',
  },
  {
    key: 'open',
    label: 'Public — no auth',
    description: 'Anyone who knows the URL can connect. No credential required.',
    whenToUse: 'Public demos, prototypes, or marketing chatbots. Never use for production apps with real data.',
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
  // Legacy: service_token_any — map to backend_service (closest meaningful option).
  if (accessMode === 'token'        && allowedPrincipals === 'both')     return 'backend_service';

  return undefined; // unknown combination
}
