import { NextRequest, NextResponse } from 'next/server';

const BRIDGE_BASE = process.env.THE_M_API_URL || 'http://them-go-bridge:8002';

// Go routes mounted at root (no /api/v1 prefix).
// Voice: /apps/{app_slug}/{ep_slug}/voice/* — two slug segments before /voice/
const GO_ROOT_PATTERNS = [/^apps\/[^/]+\/[^/]+\/voice\//, /^a2a\//];

async function proxy(req: NextRequest, params: Promise<{ path: string[] }>) {
  const token = req.cookies.get('them_access_token')?.value;
  const { path: segments } = await params;
  const path = segments.join('/');
  const isGoRoot = GO_ROOT_PATTERNS.some(p => p.test(path));
  const url = `${BRIDGE_BASE}${isGoRoot ? '' : '/api/v1'}/${path}${req.nextUrl.search}`;

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
  // Stream audio directly without buffering; forward transcript/reply headers
  if (upstreamType.startsWith('audio/')) {
    const audioHeaders: Record<string, string> = { 'Content-Type': upstreamType, 'Transfer-Encoding': 'chunked' };
    const xTranscript = upstream.headers.get('X-Transcript');
    const xReply = upstream.headers.get('X-Reply');
    if (xTranscript) audioHeaders['X-Transcript'] = xTranscript;
    if (xReply) audioHeaders['X-Reply'] = xReply;
    return new NextResponse(upstream.body, { status: upstream.status, headers: audioHeaders });
  }
  // Stream SSE directly — do NOT buffer into JSON
  if (upstreamType.includes('text/event-stream')) {
    return new NextResponse(upstream.body, {
      status: upstream.status,
      headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', 'X-Accel-Buffering': 'no' },
    });
  }
  const data = await upstream.json().catch(() => ({}));
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
