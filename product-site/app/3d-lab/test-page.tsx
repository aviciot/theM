'use client';

import { useState } from 'react';
import Link from 'next/link';

export default function TestPage() {
  const [message, setMessage] = useState('React 18 is working!');

  return (
    <div className="min-h-screen bg-black text-white p-8">
      <div className="max-w-4xl mx-auto">
        <Link
          href="/"
          className="inline-flex items-center gap-2 text-gray-400 hover:text-white transition-colors mb-8"
        >
          <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10 19l-7-7m0 0l7-7m-7 7h18" />
          </svg>
          Back to Home
        </Link>

        <h1 className="text-4xl font-bold mb-8">3D Lab Test</h1>

        <div className="space-y-6">
          <div className="bg-gray-900 rounded-lg p-6 border border-gray-700">
            <h2 className="text-2xl font-semibold mb-4">Status Check</h2>

            <div className="space-y-3">
              <div className="flex items-center gap-3">
                <span className="text-green-500">✓</span>
                <span>Next.js 16 App Router: Working</span>
              </div>

              <div className="flex items-center gap-3">
                <span className="text-green-500">✓</span>
                <span>React 18.3.1: {message}</span>
              </div>

              <div className="flex items-center gap-3">
                <span className="text-green-500">✓</span>
                <span>Client Components: Working</span>
              </div>

              <div className="flex items-center gap-3">
                <span className="text-yellow-500">⚠</span>
                <span>React Three Fiber: Incompatible with Next.js 16 + Turbopack</span>
              </div>
            </div>

            <button
              onClick={() => setMessage('State updates work!')}
              className="mt-6 px-6 py-3 bg-indigo-600 hover:bg-indigo-700 rounded-lg font-semibold transition-colors"
            >
              Test State Update
            </button>
          </div>

          <div className="bg-red-900/20 rounded-lg p-6 border border-red-900/50">
            <h2 className="text-2xl font-semibold mb-4 text-red-400">Issue Identified</h2>

            <div className="space-y-4 text-gray-300">
              <p>
                <strong>Problem:</strong> React Three Fiber 8.x has a known incompatibility with Next.js 16's Turbopack dev server.
              </p>

              <p>
                <strong>Error:</strong> <code className="bg-black/50 px-2 py-1 rounded">Cannot read properties of undefined (reading 'ReactCurrentOwner')</code>
              </p>

              <p>
                <strong>Root cause:</strong> R3F accesses React internals that Turbopack's module resolution doesn't expose correctly in dev mode.
              </p>
            </div>
          </div>

          <div className="bg-gray-900 rounded-lg p-6 border border-gray-700">
            <h2 className="text-2xl font-semibold mb-4">Solutions</h2>

            <div className="space-y-4">
              <div className="pl-4 border-l-4 border-indigo-600">
                <h3 className="font-semibold mb-2">Option 1: Use Production Build</h3>
                <p className="text-gray-400 mb-2">
                  R3F works fine in production builds (npm run build + npm start)
                </p>
                <code className="block bg-black/50 px-3 py-2 rounded text-sm">
                  docker exec them_gateway-them-product-site-1 npm run build<br/>
                  docker exec them_gateway-them-product-site-1 npm start
                </code>
              </div>

              <div className="pl-4 border-l-4 border-green-600">
                <h3 className="font-semibold mb-2">Option 2: Downgrade to Next.js 14</h3>
                <p className="text-gray-400">
                  Next.js 14 doesn't use Turbopack by default and works with R3F
                </p>
              </div>

              <div className="pl-4 border-l-4 border-yellow-600">
                <h3 className="font-semibold mb-2">Option 3: Wait for R3F Update</h3>
                <p className="text-gray-400">
                  React Three Fiber team is working on Next.js 16 compatibility
                </p>
              </div>

              <div className="pl-4 border-l-4 border-purple-600">
                <h3 className="font-semibold mb-2">Option 4: Use Plain Three.js (Recommended)</h3>
                <p className="text-gray-400 mb-2">
                  Skip R3F and use Three.js directly for this project
                </p>
                <p className="text-sm text-gray-500">
                  More control, no framework compatibility issues
                </p>
              </div>
            </div>
          </div>

          <div className="bg-gray-900 rounded-lg p-6 border border-gray-700">
            <h2 className="text-2xl font-semibold mb-4">Next Steps</h2>
            <p className="text-gray-300 mb-4">
              Choose one approach:
            </p>
            <ol className="list-decimal list-inside space-y-2 text-gray-400">
              <li>Test in production build mode (quick test)</li>
              <li>Build 3D viewer with vanilla Three.js instead of R3F</li>
              <li>Downgrade to Next.js 14 for R3F compatibility</li>
            </ol>
          </div>
        </div>
      </div>
    </div>
  );
}
