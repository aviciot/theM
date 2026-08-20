import { NextRequest, NextResponse } from 'next/server';

const ANTHROPIC_API = 'https://api.anthropic.com/v1/messages';
const ANTHROPIC_VERSION = '2023-06-01';

// Debug-only proxy: forwards LLM requests to Anthropic server-side.
// The API key is read from X-Debug-Api-Key header — never stored or logged.
// Only used from the canvas builder debug mode.
export async function POST(req: NextRequest) {
  const apiKey = req.headers.get('x-debug-api-key');
  if (!apiKey) {
    return NextResponse.json({ error: 'Missing X-Debug-Api-Key header' }, { status: 400 });
  }

  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: 'Invalid JSON body' }, { status: 400 });
  }

  let upstream: Response;
  try {
    upstream = await fetch(ANTHROPIC_API, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'x-api-key': apiKey,
        'anthropic-version': ANTHROPIC_VERSION,
      },
      body: JSON.stringify(body),
    });
  } catch (e) {
    return NextResponse.json({ error: `Failed to reach Anthropic: ${(e as Error).message}` }, { status: 502 });
  }

  const data = await upstream.json();
  return NextResponse.json(data, { status: upstream.status });
}
