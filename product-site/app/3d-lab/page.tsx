'use client';

import dynamic from 'next/dynamic';
import { useState } from 'react';
import Link from 'next/link';

// Dynamically import GLBViewer with SSR disabled (Three.js requires browser)
const GLBViewer = dynamic(
  () => import('./components/GLBViewer').then((mod) => mod.GLBViewer),
  {
    ssr: false,
    loading: () => (
      <div className="w-full h-96 flex items-center justify-center bg-gray-900 rounded-lg border border-gray-700">
        <div className="text-center">
          <div className="w-16 h-16 border-4 border-indigo-500 border-t-transparent rounded-full animate-spin mx-auto mb-4" />
          <p className="text-gray-400">Initializing 3D viewer...</p>
        </div>
      </div>
    )
  }
);

const SAMPLE_MODELS = [
  { name: 'the-M Logo Premium', path: '/models/theM_logo_premium.glb' },
  { name: 'the-M Logo AI Mesh', path: '/models/theM_logo_ai_mesh.glb' },
];

export default function ThreeDLabPage() {
  const [selectedModel, setSelectedModel] = useState('/models/theM_logo_premium.glb');
  const [customPath, setCustomPath] = useState('');

  const handleLoadCustom = () => {
    if (customPath.trim()) {
      setSelectedModel(customPath.trim());
    }
  };

  return (
    <div className="min-h-screen bg-black text-white p-8">
      <div className="max-w-7xl mx-auto">
        {/* Header */}
        <div className="mb-8">
          <Link
            href="/"
            className="inline-flex items-center gap-2 text-gray-400 hover:text-white transition-colors mb-4"
          >
            <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10 19l-7-7m0 0l7-7m-7 7h18" />
            </svg>
            Back to Home
          </Link>

          <h1 className="text-4xl font-bold mb-2">3D Lab</h1>
          <p className="text-gray-400">
            Test React Three Fiber integration with GLB models
          </p>
        </div>

        <div className="grid md:grid-cols-3 gap-8">
          {/* Left: Controls */}
          <div className="space-y-6">
            <div className="bg-gray-900 rounded-lg p-6 border border-gray-700">
              <h2 className="text-xl font-semibold mb-4">Model Selection</h2>

              <div className="space-y-3 mb-6">
                {SAMPLE_MODELS.map((model) => (
                  <button
                    key={model.path}
                    onClick={() => setSelectedModel(model.path)}
                    className={`w-full text-left px-4 py-3 rounded-lg border transition-colors ${
                      selectedModel === model.path
                        ? 'bg-indigo-600 border-indigo-500'
                        : 'bg-gray-800 border-gray-700 hover:border-gray-600'
                    }`}
                  >
                    {model.name}
                  </button>
                ))}
              </div>

              <div className="pt-6 border-t border-gray-700">
                <label className="block text-sm font-medium mb-2">
                  Custom Model Path
                </label>
                <input
                  type="text"
                  value={customPath}
                  onChange={(e) => setCustomPath(e.target.value)}
                  placeholder="/models/your-model.glb"
                  className="w-full px-4 py-2 bg-gray-800 border border-gray-700 rounded-lg focus:outline-none focus:border-indigo-500 mb-3"
                />
                <button
                  onClick={handleLoadCustom}
                  className="w-full px-4 py-2 bg-indigo-600 hover:bg-indigo-700 rounded-lg font-semibold transition-colors"
                >
                  Load Model
                </button>
              </div>
            </div>

            {/* Info Card */}
            <div className="bg-gray-900 rounded-lg p-6 border border-gray-700">
              <h3 className="text-lg font-semibold mb-3">Setup Instructions</h3>
              <div className="text-sm text-gray-400 space-y-2">
                <p>
                  <strong className="text-white">1.</strong> Place GLB files in:
                </p>
                <code className="block bg-gray-800 px-3 py-2 rounded text-xs">
                  /opt/docker/them/product-site/public/models/
                </code>

                <p className="mt-4">
                  <strong className="text-white">2.</strong> Access via:
                </p>
                <code className="block bg-gray-800 px-3 py-2 rounded text-xs">
                  /models/filename.glb
                </code>

                <p className="mt-4 text-xs text-gray-500">
                  Viewer supports rotation, zoom, and pan with mouse/touch.
                </p>
              </div>
            </div>
          </div>

          {/* Right: 3D Viewer */}
          <div className="md:col-span-2">
            <div className="bg-gray-900 rounded-lg p-6 border border-gray-700">
              <div className="flex items-center justify-between mb-4">
                <h2 className="text-xl font-semibold">3D Viewer</h2>
                <div className="text-sm text-gray-500">
                  Current: {selectedModel}
                </div>
              </div>

              <GLBViewer modelPath={selectedModel} className="w-full h-[600px]" />

              <div className="mt-4 p-4 bg-gray-800/50 rounded-lg border border-gray-700">
                <h3 className="text-sm font-semibold mb-2">Controls</h3>
                <ul className="text-sm text-gray-400 space-y-1">
                  <li>• <strong>Left Click + Drag:</strong> Rotate</li>
                  <li>• <strong>Right Click + Drag:</strong> Pan</li>
                  <li>• <strong>Scroll:</strong> Zoom</li>
                </ul>
              </div>
            </div>
          </div>
        </div>

        {/* Tech Stack Info */}
        <div className="mt-8 p-6 bg-gray-900 rounded-lg border border-gray-700">
          <h3 className="text-lg font-semibold mb-3">Stack Info</h3>
          <div className="grid md:grid-cols-3 gap-4 text-sm">
            <div>
              <div className="text-gray-500">Framework</div>
              <div className="font-mono">Next.js 16.2 + React 19</div>
            </div>
            <div>
              <div className="text-gray-500">3D Rendering</div>
              <div className="font-mono">Three.js ^0.170 + R3F ^8.18</div>
            </div>
            <div>
              <div className="text-gray-500">Utilities</div>
              <div className="font-mono">@react-three/drei ^9.120</div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
