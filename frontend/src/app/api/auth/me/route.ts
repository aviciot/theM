import { NextRequest, NextResponse } from 'next/server';

const AUTH_BASE = process.env.THE_M_AUTH_URL || 'http://them-auth-go:8703';

export async function GET(req: NextRequest) {
  const token = req.cookies.get('them_access_token')?.value;
  if (!token) {
    return NextResponse.json({ detail: 'Not authenticated' }, { status: 401 });
  }

  const upstream = await fetch(`${AUTH_BASE}/api/v1/auth/me`, {
    headers: { Cookie: `them_access_token=${token}` },
  });

  const data = await upstream.json();
  return NextResponse.json(data, { status: upstream.status });
}
