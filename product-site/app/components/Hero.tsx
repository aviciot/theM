'use client';

import { useEffect, useRef } from 'react';
import { motion } from 'framer-motion';
import { gsap } from 'gsap';
import { ScrollTrigger } from 'gsap/ScrollTrigger';

gsap.registerPlugin(ScrollTrigger);

export function Hero() {
  const bgRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!bgRef.current) return;

    // Parallax background
    gsap.to(bgRef.current, {
      y: 300,
      ease: 'none',
      scrollTrigger: {
        trigger: bgRef.current,
        start: 'top top',
        end: 'bottom top',
        scrub: true,
      },
    });
  }, []);

  return (
    <section className="relative min-h-screen flex items-center justify-center overflow-hidden">
      {/* Animated background */}
      <div
        ref={bgRef}
        className="absolute inset-0 bg-gradient-to-br from-indigo-600 via-purple-600 to-pink-600 opacity-10"
      />

      {/* Grid pattern overlay */}
      <div className="absolute inset-0 bg-[linear-gradient(to_right,#80808012_1px,transparent_1px),linear-gradient(to_bottom,#80808012_1px,transparent_1px)] bg-[size:64px_64px]" />

      {/* Content */}
      <div className="relative z-10 max-w-5xl mx-auto px-6 text-center">
        <motion.div
          initial={{ opacity: 0, y: 20 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.8, ease: [0.4, 0.0, 0.2, 1] }}
        >
          <h1 className="text-6xl md:text-8xl font-bold mb-6 bg-clip-text text-transparent bg-gradient-to-r from-white to-gray-400">
            Ship AI Products
            <br />
            <span className="text-5xl md:text-7xl">10x Faster</span>
          </h1>

          <p className="text-xl md:text-2xl text-gray-400 mb-12 max-w-2xl mx-auto">
            Visual workflow builder that turns AI experiments into production systems — without writing code
          </p>

          <div className="flex gap-4 justify-center">
            <motion.a
              href="#features"
              whileHover={{ scale: 1.05 }}
              whileTap={{ scale: 0.95 }}
              className="px-8 py-4 bg-indigo-600 text-white rounded-lg font-semibold hover:bg-indigo-700 transition-colors"
            >
              See How It Works
            </motion.a>

            <motion.a
              href="https://aviciot.github.io/theM/"
              target="_blank"
              rel="noopener noreferrer"
              whileHover={{ scale: 1.05 }}
              whileTap={{ scale: 0.95 }}
              className="px-8 py-4 bg-white/10 backdrop-blur-sm text-white rounded-lg font-semibold border border-white/20 hover:bg-white/20 transition-colors"
            >
              Read Docs
            </motion.a>
          </div>
        </motion.div>

        {/* Video placeholder - will be replaced with user's video */}
        <motion.div
          initial={{ opacity: 0, y: 40 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.8, delay: 0.3, ease: [0.4, 0.0, 0.2, 1] }}
          className="mt-20"
        >
          <div className="relative rounded-2xl overflow-hidden shadow-2xl border border-white/10 bg-black/20 backdrop-blur-sm">
            {/* Placeholder for video */}
            <div className="aspect-video flex items-center justify-center text-gray-500">
              <div className="text-center">
                <svg
                  className="w-24 h-24 mx-auto mb-4 opacity-50"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeWidth={2}
                    d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"
                  />
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeWidth={2}
                    d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
                  />
                </svg>
                <p className="text-sm">Demo video will be embedded here</p>
                <p className="text-xs mt-2 opacity-50">
                  Replace with: /public/video/demo.mp4
                </p>
              </div>
            </div>

            {/* Uncomment when video is added:
            <video
              autoPlay
              loop
              muted
              playsInline
              className="w-full"
            >
              <source src="/video/demo.mp4" type="video/mp4" />
            </video>
            */}
          </div>
        </motion.div>

        {/* Scroll indicator */}
        <motion.div
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          transition={{ duration: 0.8, delay: 0.6 }}
          className="mt-20"
        >
          <div className="flex flex-col items-center gap-2 text-gray-500">
            <span className="text-sm">Scroll to explore</span>
            <motion.div
              animate={{ y: [0, 8, 0] }}
              transition={{ duration: 1.5, repeat: Infinity, ease: 'easeInOut' }}
            >
              <svg
                className="w-6 h-6"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth={2}
                  d="M19 14l-7 7m0 0l-7-7m7 7V3"
                />
              </svg>
            </motion.div>
          </div>
        </motion.div>
      </div>
    </section>
  );
}
