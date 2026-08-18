import { NextRequest, NextResponse } from 'next/server';

const AUTH_BASE = process.env.THE_M_AUTH_URL || 'http://them-auth-go:8703';

async function proxy(req: NextRequest, method: string) {
  const token = req.cookies.get('them_access_token')?.value;
  if (!token) {
    return NextResponse.json({ detail: 'Not authenticated' }, { status: 401 });
  }

  const init: RequestInit = {
    method,
    headers: { Cookie: `them_access_token=${token}` },
  };
  if (method === 'PUT') {
    const body = await req.text();
    (init.headers as Record<string, string>)['Content-Type'] = 'application/json';
    init.body = body;
  }

  const upstream = await fetch(`${AUTH_BASE}/api/v1/auth/me/preferences`, init);
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { 'Content-Type': 'application/json' },
  });
}

export async function GET(req: NextRequest) { return proxy(req, 'GET'); }
export async function PUT(req: NextRequest) { return proxy(req, 'PUT'); }
