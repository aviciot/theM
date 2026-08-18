import { NextRequest, NextResponse } from 'next/server';

const AUTH_BASE = process.env.THE_M_AUTH_URL || 'http://them-auth-go:8703';

export async function POST(req: NextRequest) {
  const refreshToken = req.cookies.get('them_refresh_token')?.value;
  if (!refreshToken) {
    return NextResponse.json({ detail: 'No refresh token' }, { status: 401 });
  }

  const upstream = await fetch(`${AUTH_BASE}/api/v1/auth/refresh`, {
    method: 'POST',
    headers: { Cookie: `them_refresh_token=${refreshToken}` },
  });

  const data = await upstream.json();
  if (!upstream.ok) {
    return NextResponse.json(data, { status: upstream.status });
  }

  const { access_token, refresh_token, expires_in } = data;
  const res = NextResponse.json({ ok: true, expires_in });

  const secure = process.env.COOKIE_SECURE !== 'false' && process.env.NODE_ENV === 'production';
  res.cookies.set('them_access_token', access_token, {
    httpOnly: true,
    sameSite: 'strict',
    secure,
    maxAge: expires_in,
    path: '/',
  });
  res.cookies.set('them_refresh_token', refresh_token, {
    httpOnly: true,
    sameSite: 'strict',
    secure,
    maxAge: 60 * 60 * 24 * 7,
    path: '/',
  });

  return res;
}
