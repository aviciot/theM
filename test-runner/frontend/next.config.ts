import type { NextConfig } from 'next';

const nextConfig: NextConfig = {
  output: 'standalone',
  // Allow external IP access in dev mode (hot reload)
  allowedDevOrigins: ['10.55.125.43'],
  async rewrites() {
    return [
      {
        source: '/api/backend/:path*',
        destination: `${process.env.BACKEND_URL ?? 'http://tr-backend:8090'}/:path*`,
      },
    ];
  },
};

export default nextConfig;
