// Shared JWT utility — base64url → JSON payload
export function decodeJwt(token: string): Record<string, any> {
  const seg = token.split('.')[1];
  const b64 = seg.replace(/-/g, '+').replace(/_/g, '/');
  const padded = b64 + '='.repeat((4 - b64.length % 4) % 4);
  return JSON.parse(atob(padded));
}

export function isTokenValid(token: string | null): boolean {
  if (!token) return false;
  try {
    const payload = decodeJwt(token);
    return payload.exp * 1000 > Date.now() - 10_000;
  } catch {
    return false;
  }
}
