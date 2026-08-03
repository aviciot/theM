/**
 * Returns a valid access token for WS URLs (playground only).
 * Auto-refreshes if the current token is expired or missing.
 */
import { NextRequest, NextResponse } from 'next/server';

function jwtExpiresIn(token: string): number {
  try {
    const payload = JSON.parse(Buffer.from(token.split('.')[1], 'base64url').toString());
    return (payload.exp ?? 0) - Math.floor(Date.now() / 1000);
  } catch {
    return -1;
  }
}

const AUTH_BASE = process.env.THE_M_AUTH_URL || 'http://them-auth-go:8703';

export async function GET(req: NextRequest) {
  const token = req.cookies.get('them_access_token')?.value;
  const refreshToken = req.cookies.get('them_refresh_token')?.value;

  // Token is valid with > 30s left — return it directly
  if (token && jwtExpiresIn(token) > 30) {
    return NextResponse.json({ token });
  }

  // Try to refresh
  if (!refreshToken) {
    return NextResponse.json({ error: 'Not authenticated' }, { status: 401 });
  }

  const upstream = await fetch(`${AUTH_BASE}/api/v1/auth/refresh`, {
    method: 'POST',
    headers: { Cookie: `them_refresh_token=${refreshToken}` },
  });

  if (!upstream.ok) {
    return NextResponse.json({ error: 'Session expired' }, { status: 401 });
  }

  const data = await upstream.json();
  const { access_token, refresh_token, expires_in } = data;

  const res = NextResponse.json({ token: access_token });
  res.cookies.set('them_access_token', access_token, {
    httpOnly: true,
    sameSite: 'strict',
    secure: process.env.NODE_ENV === 'production',
    maxAge: expires_in,
    path: '/',
  });
  res.cookies.set('them_refresh_token', refresh_token, {
    httpOnly: true,
    sameSite: 'strict',
    secure: process.env.NODE_ENV === 'production',
    maxAge: 60 * 60 * 24 * 7,
    path: '/',
  });
  return res;
}
