import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  images: {
    unoptimized: true,
  },
  transpilePackages: ['three', '@react-three/fiber', '@react-three/drei'],
  // Allow HMR from remote IP
  allowedDevOrigins: ['10.55.125.43'],
};

export default nextConfig;
