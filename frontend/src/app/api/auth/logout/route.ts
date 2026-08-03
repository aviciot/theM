import { NextRequest, NextResponse } from 'next/server';

const AUTH_BASE = process.env.THE_M_AUTH_URL || 'http://them-auth-go:8703';

export async function POST(req: NextRequest) {
  const token = req.cookies.get('them_access_token')?.value;

  if (token) {
    await fetch(`${AUTH_BASE}/api/v1/auth/logout`, {
      method: 'POST',
      headers: { Cookie: `them_access_token=${token}` },
    }).catch(() => {});
  }

  const res = NextResponse.json({ ok: true });
  res.cookies.delete('them_access_token');
  res.cookies.delete('them_refresh_token');
  return res;
}
