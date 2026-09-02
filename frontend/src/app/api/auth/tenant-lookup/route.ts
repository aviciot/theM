import { NextRequest, NextResponse } from 'next/server';

const AUTH_BASE = process.env.THE_M_AUTH_URL || 'http://them-auth-go:8703';

export async function GET(req: NextRequest) {
  const email = req.nextUrl.searchParams.get('email') ?? '';
  const upstream = await fetch(
    `${AUTH_BASE}/api/v1/auth/tenant-lookup?email=${encodeURIComponent(email)}`,
    { method: 'GET' },
  ).catch(() => null);

  if (!upstream) {
    return NextResponse.json({ detail: 'auth service unavailable' }, { status: 503 });
  }

  const data = await upstream.json().catch(() => ({}));
  return NextResponse.json(data, { status: upstream.status });
}
