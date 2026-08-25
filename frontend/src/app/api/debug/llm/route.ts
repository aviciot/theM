import { NextRequest, NextResponse } from 'next/server';

// Debug-only proxy: forwards LLM requests to provider APIs server-side.
// API key read from X-Debug-Api-Key, provider from X-Debug-Provider.
// Never stored or logged. Only used from the canvas builder debug mode.

interface ProviderConfig {
  url: string;
  buildHeaders: (apiKey: string) => Record<string, string>;
  buildBody: (body: unknown) => unknown;
}

const PROVIDERS: Record<string, ProviderConfig> = {
  anthropic: {
    url: 'https://api.anthropic.com/v1/messages',
    buildHeaders: (apiKey) => ({
      'Content-Type': 'application/json',
      'x-api-key': apiKey,
      'anthropic-version': '2023-06-01',
    }),
    buildBody: (body) => body,
  },
  openai: {
    url: 'https://api.openai.com/v1/chat/completions',
    buildHeaders: (apiKey) => ({
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${apiKey}`,
    }),
    // OpenAI uses chat completions format — convert from Anthropic-style body
    buildBody: (body) => {
      const b = body as Record<string, unknown>;
      const messages = b.messages as { role: string; content: string }[] ?? [];
      const openaiMessages: { role: string; content: string }[] = [];
      if (b.system) openaiMessages.push({ role: 'system', content: String(b.system) });
      openaiMessages.push(...messages);
      return { model: b.model, max_tokens: b.max_tokens ?? 4096, messages: openaiMessages };
    },
  },
  groq: {
    url: 'https://api.groq.com/openai/v1/chat/completions',
    buildHeaders: (apiKey) => ({
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${apiKey}`,
    }),
    buildBody: (body) => {
      const b = body as Record<string, unknown>;
      const messages = b.messages as { role: string; content: string }[] ?? [];
      const groqMessages: { role: string; content: string }[] = [];
      if (b.system) groqMessages.push({ role: 'system', content: String(b.system) });
      groqMessages.push(...messages);
      return { model: b.model, max_tokens: b.max_tokens ?? 4096, messages: groqMessages };
    },
  },
  gemini: {
    url: '',  // built dynamically — model is in the URL
    buildHeaders: () => ({ 'Content-Type': 'application/json' }),
    buildBody: (body) => {
      const b = body as Record<string, unknown>;
      const messages = b.messages as { role: string; content: string }[] ?? [];
      const parts = messages.map(m => ({ text: m.content }));
      const contents = [{ role: 'user', parts }];
      if (b.system) {
        return {
          system_instruction: { parts: [{ text: String(b.system) }] },
          contents,
          generationConfig: { maxOutputTokens: b.max_tokens ?? 4096 },
        };
      }
      return { contents, generationConfig: { maxOutputTokens: b.max_tokens ?? 4096 } };
    },
  },
};

// Normalize provider response to Anthropic-style: { content: [{ type: 'text', text: '...' }] }
function normalizeResponse(provider: string, data: unknown): unknown {
  if (provider === 'anthropic') return data;
  if (provider === 'openai' || provider === 'groq') {
    const d = data as Record<string, unknown>;
    const choice = (d.choices as { message: { content: string } }[])?.[0];
    const text = choice?.message?.content ?? '';
    return { content: [{ type: 'text', text }] };
  }
  if (provider === 'gemini') {
    const d = data as Record<string, unknown>;
    const candidate = (d.candidates as { content: { parts: { text: string }[] } }[])?.[0];
    const text = candidate?.content?.parts?.[0]?.text ?? '';
    return { content: [{ type: 'text', text }] };
  }
  return data;
}

export async function POST(req: NextRequest) {
  const apiKey = req.headers.get('x-debug-api-key');
  const provider = (req.headers.get('x-debug-provider') ?? 'anthropic').toLowerCase();

  if (!apiKey) {
    return NextResponse.json({ error: 'Missing X-Debug-Api-Key header' }, { status: 400 });
  }

  const cfg = PROVIDERS[provider];
  if (!cfg) {
    return NextResponse.json({ error: `Unknown provider: ${provider}` }, { status: 400 });
  }

  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: 'Invalid JSON body' }, { status: 400 });
  }

  let upstreamUrl = cfg.url;
  if (provider === 'gemini') {
    const model = (body as Record<string, unknown>).model as string ?? 'gemini-2.0-flash';
    upstreamUrl = `https://generativelanguage.googleapis.com/v1beta/models/${model}:generateContent?key=${apiKey}`;
  }

  let upstream: Response;
  try {
    upstream = await fetch(upstreamUrl, {
      method: 'POST',
      headers: cfg.buildHeaders(apiKey),
      body: JSON.stringify(cfg.buildBody(body)),
    });
  } catch (e) {
    return NextResponse.json({ error: `Failed to reach ${provider}: ${(e as Error).message}` }, { status: 502 });
  }

  const data = await upstream.json();
  if (!upstream.ok) {
    return NextResponse.json(data, { status: upstream.status });
  }
  return NextResponse.json(normalizeResponse(provider, data), { status: 200 });
}
