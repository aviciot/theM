/**
 * Returns the raw access token for use in WS URLs (playground only).
 * Safe because it's only used transiently to open a WS — never stored in JS.
 */
import { NextRequest, NextResponse } from 'next/server';

export async function GET(req: NextRequest) {
  const token = req.cookies.get('odin_access_token')?.value;
  if (!token) return NextResponse.json({ error: 'Not authenticated' }, { status: 401 });
  return NextResponse.json({ token });
}
