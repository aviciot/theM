import { NextRequest, NextResponse } from 'next/server';

const BRIDGE_BASE = process.env.ODIN_API_URL || 'http://odin-bridge:8001';

async function proxy(req: NextRequest, params: Promise<{ path: string[] }>) {
  const token = req.cookies.get('odin_access_token')?.value;
  const { path: segments } = await params;
  const path = segments.join('/');
  const url = `${BRIDGE_BASE}/api/v1/${path}${req.nextUrl.search}`;

  const contentType = req.headers.get('content-type') || '';
  const isMultipart = contentType.includes('multipart/form-data');
  const headers: Record<string, string> = {};
  if (!isMultipart) headers['Content-Type'] = 'application/json';
  if (isMultipart) headers['Content-Type'] = contentType; // preserve boundary
  if (token) headers['Authorization'] = `Bearer ${token}`;

  const body = req.method !== 'GET' && req.method !== 'HEAD'
    ? await req.arrayBuffer()
    : undefined;

  const upstream = await fetch(url, { method: req.method, headers, body });
  const upstreamType = upstream.headers.get('content-type') || '';
  if (upstream.status === 204) return new NextResponse(null, { status: 204 });
  if (upstreamType.startsWith('audio/')) {
    const buf = await upstream.arrayBuffer();
    return new NextResponse(buf, { status: upstream.status, headers: { 'Content-Type': upstreamType } });
  }
  const data = await upstream.json().catch(() => null);
  return NextResponse.json(data, { status: upstream.status });
}

export async function GET(req: NextRequest, { params }: { params: Promise<{ path: string[] }> }) {
  return proxy(req, params);
}
export async function POST(req: NextRequest, { params }: { params: Promise<{ path: string[] }> }) {
  return proxy(req, params);
}
export async function PUT(req: NextRequest, { params }: { params: Promise<{ path: string[] }> }) {
  return proxy(req, params);
}
export async function PATCH(req: NextRequest, { params }: { params: Promise<{ path: string[] }> }) {
  return proxy(req, params);
}
export async function DELETE(req: NextRequest, { params }: { params: Promise<{ path: string[] }> }) {
  return proxy(req, params);
}
