'use client';

import { useEffect, useRef } from 'react';
import { motion } from 'framer-motion';
import { gsap } from 'gsap';
import { ScrollTrigger } from 'gsap/ScrollTrigger';

gsap.registerPlugin(ScrollTrigger);

export function SolutionSection() {
  const imgRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!imgRef.current) return;

    gsap.fromTo(
      imgRef.current,
      { opacity: 0, scale: 0.95 },
      {
        opacity: 1,
        scale: 1,
        duration: 1,
        ease: 'power2.out',
        scrollTrigger: {
          trigger: imgRef.current,
          start: 'top 80%',
        },
      }
    );
  }, []);

  return (
    <section className="py-32 px-6 bg-gray-900">
      <div className="max-w-7xl mx-auto">
        <div className="grid md:grid-cols-2 gap-16 items-center">
          {/* Left: Text */}
          <div>
            <motion.div
              initial={{ opacity: 0, y: 20 }}
              whileInView={{ opacity: 1, y: 0 }}
              viewport={{ once: true }}
              transition={{ duration: 0.6 }}
              className="inline-block px-4 py-2 bg-indigo-500/10 border border-indigo-500/20 rounded-full text-indigo-400 text-sm font-semibold mb-6"
            >
              The Solution
            </motion.div>

            <motion.h2
              initial={{ opacity: 0, y: 20 }}
              whileInView={{ opacity: 1, y: 0 }}
              viewport={{ once: true }}
              transition={{ duration: 0.6, delay: 0.1 }}
              className="text-4xl md:text-5xl font-bold mb-6"
            >
              Visual Workflow Builder
              <br />
              <span className="text-indigo-400">No Code Required</span>
            </motion.h2>

            <motion.div
              initial={{ opacity: 0, y: 20 }}
              whileInView={{ opacity: 1, y: 0 }}
              viewport={{ once: true }}
              transition={{ duration: 0.6, delay: 0.2 }}
              className="space-y-4 text-lg text-gray-300"
            >
              <p>
                <strong className="text-white">Draw your AI workflows</strong> instead of coding them.
                Connect AI models, APIs, and business logic with drag-and-drop.
              </p>

              <p>
                Changes go live <strong className="text-white">instantly</strong> — no deployments,
                no waiting on engineering.
              </p>

              <p>
                Built-in <strong className="text-white">multi-tenant isolation</strong> means you can
                safely serve thousands of customers from one platform.
              </p>
            </motion.div>

            <motion.div
              initial={{ opacity: 0, y: 20 }}
              whileInView={{ opacity: 1, y: 0 }}
              viewport={{ once: true }}
              transition={{ duration: 0.6, delay: 0.3 }}
              className="mt-8 space-y-3"
            >
              {[
                { icon: '⚡', text: 'Ship in days, not months' },
                { icon: '💰', text: 'Track every AI dollar spent' },
                { icon: '🔐', text: 'Enterprise-grade security built-in' },
              ].map((item, i) => (
                <div key={i} className="flex items-center gap-3">
                  <span className="text-2xl">{item.icon}</span>
                  <span className="text-gray-300">{item.text}</span>
                </div>
              ))}
            </motion.div>
          </div>

          {/* Right: Visual */}
          <div ref={imgRef}>
            <div className="relative">
              {/* Placeholder for Agent Canvas screenshot */}
              <div className="rounded-2xl overflow-hidden border border-white/10 bg-gradient-to-br from-indigo-500/20 to-purple-500/20 backdrop-blur-sm">
                <div className="aspect-[4/3] flex items-center justify-center p-8">
                  <div className="text-center">
                    <svg
                      className="w-32 h-32 mx-auto mb-6 text-indigo-400/50"
                      fill="none"
                      stroke="currentColor"
                      viewBox="0 0 24 24"
                    >
                      <path
                        strokeLinecap="round"
                        strokeLinejoin="round"
                        strokeWidth={1.5}
                        d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z"
                      />
                    </svg>
                    <p className="text-gray-500 text-sm">
                      Agent Canvas Screenshot
                    </p>
                    <p className="text-gray-600 text-xs mt-2">
                      Add: /public/images/canvas-screenshot.png
                    </p>
                  </div>
                </div>
              </div>

              {/* Floating elements for visual interest */}
              <motion.div
                animate={{ y: [0, -10, 0] }}
                transition={{ duration: 3, repeat: Infinity, ease: 'easeInOut' }}
                className="absolute -top-4 -right-4 px-4 py-2 bg-green-500/20 border border-green-500/30 rounded-lg text-green-400 text-sm font-semibold backdrop-blur-sm"
              >
                ✓ Live in 10 minutes
              </motion.div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
