import type { NextConfig } from 'next';

const nextConfig: NextConfig = {
  output: 'standalone',
  allowedDevOrigins: ['10.55.125.43'],
  async rewrites() {
    const apiBase = process.env.THE_M_API_URL || 'http://them-bridge:8001';
    return [
      { source: '/api/bridge/:path*', destination: `${apiBase}/:path*` },
    ];
  },
};

export default nextConfig;
