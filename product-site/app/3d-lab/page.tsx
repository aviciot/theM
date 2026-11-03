'use client';

import dynamic from 'next/dynamic';

// Import entire page client-side to avoid SSR issues
const PageContent = dynamic(
  () => import('./page-content').then((mod) => mod.PageContent),
  {
    ssr: false,
    loading: () => (
      <div className="min-h-screen bg-black text-white flex items-center justify-center">
        <div className="text-center">
          <div className="w-16 h-16 border-4 border-indigo-500 border-t-transparent rounded-full animate-spin mx-auto mb-4" />
          <p className="text-gray-400">Loading 3D Lab...</p>
        </div>
      </div>
    )
  }
);

export default function ThreeDLabPage() {
  return <PageContent />;
}
