'use client';

import { useEffect, useRef } from 'react';
import { motion } from 'framer-motion';
import { gsap } from 'gsap';
import { ScrollTrigger } from 'gsap/ScrollTrigger';

gsap.registerPlugin(ScrollTrigger);

const features = [
  {
    title: 'Visual Agent Builder',
    description: 'Drag-and-drop workflow creation. No code required. Build complex AI agents in minutes.',
    icon: '🎨',
  },
  {
    title: 'Multi-Tenant Security',
    description: 'Complete data isolation with Row-Level Security. Each tenant gets their own isolated workspace.',
    icon: '🔐',
  },
  {
    title: 'LLM Gateway',
    description: 'Unified access to Claude, GPT, and custom models. Built-in cost tracking and governance.',
    icon: '🤖',
  },
  {
    title: 'Agent-to-Agent (A2A)',
    description: 'Agents can call other agents. Build specialized agents that work together seamlessly.',
    icon: '🔗',
  },
  {
    title: 'Production Ready',
    description: 'Temporal workflows for reliability. Event streaming. Survives crashes and scales effortlessly.',
    icon: '⚡',
  },
  {
    title: 'Built-in Observability',
    description: 'Track costs, usage, and performance. Real-time monitoring with ClickHouse events.',
    icon: '📊',
  },
];

export function Features() {
  const sectionRef = useRef<HTMLElement>(null);
  const cardsRef = useRef<HTMLDivElement[]>([]);

  useEffect(() => {
    if (!sectionRef.current) return;

    cardsRef.current.forEach((card, index) => {
      if (!card) return;

      gsap.fromTo(
        card,
        { opacity: 0, y: 50 },
        {
          opacity: 1,
          y: 0,
          duration: 0.6,
          delay: index * 0.1,
          ease: 'power2.out',
          scrollTrigger: {
            trigger: card,
            start: 'top 85%',
          },
        }
      );
    });
  }, []);

  return (
    <section ref={sectionRef} id="features" className="py-32 px-6">
      <div className="max-w-7xl mx-auto">
        <div className="text-center mb-20">
          <motion.h2
            initial={{ opacity: 0, y: 20 }}
            whileInView={{ opacity: 1, y: 0 }}
            viewport={{ once: true }}
            transition={{ duration: 0.6 }}
            className="text-5xl md:text-6xl font-bold mb-6"
          >
            Everything You Need
          </motion.h2>

          <motion.p
            initial={{ opacity: 0, y: 20 }}
            whileInView={{ opacity: 1, y: 0 }}
            viewport={{ once: true }}
            transition={{ duration: 0.6, delay: 0.1 }}
            className="text-xl text-gray-400 max-w-2xl mx-auto"
          >
            Build, deploy, and run AI agent workflows with enterprise-grade security and scalability
          </motion.p>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-8">
          {features.map((feature, index) => (
            <div
              key={index}
              ref={(el) => {
                if (el) cardsRef.current[index] = el;
              }}
              className="group"
            >
              <motion.div
                whileHover={{ y: -8 }}
                transition={{ duration: 0.3 }}
                className="h-full p-8 rounded-2xl bg-white/5 backdrop-blur-sm border border-white/10 hover:border-indigo-500/50 transition-colors"
              >
                <div className="text-5xl mb-4">{feature.icon}</div>
                <h3 className="text-2xl font-semibold mb-3">{feature.title}</h3>
                <p className="text-gray-400 leading-relaxed">{feature.description}</p>
              </motion.div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
