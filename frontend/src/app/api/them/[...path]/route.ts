import { NextRequest, NextResponse } from 'next/server';

const BRIDGE_BASE = process.env.THE_M_API_URL || 'http://them-bridge:8001';
const GO_BRIDGE_BASE = process.env.THE_M_GO_API_URL || 'http://them-go-bridge:8002';

// Paths that only exist in the Go bridge — forward these directly instead of Python.
// Pattern: substring match on the joined path (no leading slash).
const GO_ONLY_PREFIXES = [
  'admin/agent-definitions',
  'admin/node-types',
  'admin/transform-functions',
  'admin/transform-test',
  'admin/transform-assist',
  'admin/mcp-servers',
  'admin/component-definitions',
];
// Sub-path patterns that must also go to Go regardless of prefix.
const GO_ONLY_PATTERNS = [
  /\/agent-bindings/,
  /\/agents\/[^/]+\/params/,
  /\/agents\/[^/]+\/llm-nodes/,
  /\/provider-keys/,
  /\/test-llm/,
  /\/orchestrators\/[^/]+\/llm/,
  /^apps\/[^/]+\/voice\//,
];

function resolveBase(path: string): string {
  if (GO_ONLY_PREFIXES.some(p => path.startsWith(p))) return GO_BRIDGE_BASE;
  if (GO_ONLY_PATTERNS.some(p => p.test(path))) return GO_BRIDGE_BASE;
  return BRIDGE_BASE;
}

async function proxy(req: NextRequest, params: Promise<{ path: string[] }>) {
  const token = req.cookies.get('them_access_token')?.value;
  const { path: segments } = await params;
  const path = segments.join('/');
  const base = resolveBase(path);
  const url = `${base}/api/v1/${path}${req.nextUrl.search}`;

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
