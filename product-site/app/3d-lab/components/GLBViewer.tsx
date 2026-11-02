'use client';

import { Suspense, useState } from 'react';
import { Canvas } from '@react-three/fiber';
import { OrbitControls, useGLTF, Environment, PerspectiveCamera } from '@react-three/drei';
import * as THREE from 'three';

interface ModelProps {
  url: string;
  onError: (error: Error) => void;
}

function Model({ url, onError }: ModelProps) {
  try {
    const { scene } = useGLTF(url);

    // Center and scale model
    const box = new THREE.Box3().setFromObject(scene);
    const center = box.getCenter(new THREE.Vector3());
    const size = box.getSize(new THREE.Vector3());
    const maxDim = Math.max(size.x, size.y, size.z);
    const scale = 2 / maxDim;

    scene.position.sub(center);
    scene.scale.setScalar(scale);

    return <primitive object={scene} />;
  } catch (error) {
    onError(error as Error);
    return null;
  }
}

function LoadingSpinner() {
  return (
    <div className="absolute inset-0 flex items-center justify-center bg-gray-900/80 backdrop-blur-sm">
      <div className="text-center">
        <div className="w-16 h-16 border-4 border-indigo-500 border-t-transparent rounded-full animate-spin mx-auto mb-4" />
        <p className="text-gray-400">Loading 3D model...</p>
      </div>
    </div>
  );
}

interface GLBViewerProps {
  modelPath: string;
  className?: string;
}

export function GLBViewer({ modelPath, className = 'w-full h-96' }: GLBViewerProps) {
  const [error, setError] = useState<string | null>(null);
  const [webGLSupported, setWebGLSupported] = useState(true);

  // Check WebGL support
  if (typeof window !== 'undefined' && webGLSupported) {
    try {
      const canvas = document.createElement('canvas');
      const gl = canvas.getContext('webgl') || canvas.getContext('experimental-webgl');
      if (!gl) {
        setWebGLSupported(false);
      }
    } catch (e) {
      setWebGLSupported(false);
    }
  }

  if (!webGLSupported) {
    return (
      <div className={`${className} flex items-center justify-center bg-gray-900 rounded-lg border border-gray-700`}>
        <div className="text-center px-6">
          <svg className="w-16 h-16 mx-auto mb-4 text-gray-600" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
          </svg>
          <p className="text-gray-400 font-semibold mb-2">WebGL Not Supported</p>
          <p className="text-gray-500 text-sm">Your browser doesn't support 3D graphics.</p>
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className={`${className} flex items-center justify-center bg-gray-900 rounded-lg border border-red-900/50`}>
        <div className="text-center px-6">
          <svg className="w-16 h-16 mx-auto mb-4 text-red-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <p className="text-red-400 font-semibold mb-2">Failed to Load Model</p>
          <p className="text-gray-500 text-sm">{error}</p>
        </div>
      </div>
    );
  }

  return (
    <div className={`${className} relative bg-gray-900 rounded-lg overflow-hidden border border-gray-700`}>
      <Canvas
        shadows
        camera={{ position: [0, 0, 5], fov: 50 }}
        gl={{
          antialias: true,
          alpha: true,
          preserveDrawingBuffer: true
        }}
      >
        <PerspectiveCamera makeDefault position={[0, 0, 5]} />

        {/* Lighting */}
        <ambientLight intensity={0.5} />
        <directionalLight
          position={[10, 10, 5]}
          intensity={1}
          castShadow
          shadow-mapSize-width={2048}
          shadow-mapSize-height={2048}
        />
        <directionalLight position={[-10, -10, -5]} intensity={0.3} />

        {/* Environment for reflections */}
        <Environment preset="studio" />

        {/* Model */}
        <Suspense fallback={null}>
          <Model url={modelPath} onError={(e) => setError(e.message)} />
        </Suspense>

        {/* Controls */}
        <OrbitControls
          enablePan={true}
          enableZoom={true}
          enableRotate={true}
          minDistance={2}
          maxDistance={10}
          autoRotate={false}
        />
      </Canvas>

      <Suspense fallback={<LoadingSpinner />} />
    </div>
  );
}
