import type { NextConfig } from 'next';

const nextConfig: NextConfig = {
  async rewrites() {
    const apiBase = process.env.ODIN_API_URL || 'http://odin-bridge:8001';
    const authBase = process.env.ODIN_AUTH_URL || 'http://odin-auth-service:8701';
    return [
      { source: '/api/odin/:path*', destination: `${apiBase}/api/v1/:path*` },
      { source: '/api/bridge/:path*', destination: `${apiBase}/:path*` },
      { source: '/api/auth/:path*', destination: `${authBase}/api/v1/auth/:path*` },
    ];
  },
};

export default nextConfig;
