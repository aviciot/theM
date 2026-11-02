'use client';

import { useEffect, useRef } from 'react';
import { motion } from 'framer-motion';
import { gsap } from 'gsap';
import { ScrollTrigger } from 'gsap/ScrollTrigger';

gsap.registerPlugin(ScrollTrigger);

const benefits = [
  {
    icon: '🚀',
    title: 'Ship 10x Faster',
    stat: 'Days, not months',
    description: 'Turn ideas into production AI agents in hours. No waiting on engineering sprints.',
    color: 'from-blue-500 to-cyan-500',
  },
  {
    icon: '💰',
    title: 'Control Costs',
    stat: '100% visibility',
    description: 'See exactly what every AI call costs. Set budgets per customer, per workflow.',
    color: 'from-green-500 to-emerald-500',
  },
  {
    icon: '🔐',
    title: 'Enterprise Security',
    stat: 'Built-in isolation',
    description: 'Multi-tenant from day one. Each customer gets complete data isolation automatically.',
    color: 'from-purple-500 to-pink-500',
  },
  {
    icon: '📈',
    title: 'Scale Effortlessly',
    stat: '1 to 10,000 users',
    description: 'Production-ready infrastructure. Handles crashes, retries, and high load automatically.',
    color: 'from-orange-500 to-red-500',
  },
];

export function BenefitsSection() {
  const sectionRef = useRef<HTMLElement>(null);
  const cardsRef = useRef<HTMLDivElement[]>([]);

  useEffect(() => {
    if (!sectionRef.current) return;

    cardsRef.current.forEach((card, index) => {
      if (!card) return;

      gsap.fromTo(
        card,
        { opacity: 0, y: 40 },
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
    <section ref={sectionRef} className="py-32 px-6 bg-black">
      <div className="max-w-7xl mx-auto">
        <div className="text-center mb-20">
          <motion.div
            initial={{ opacity: 0, y: 20 }}
            whileInView={{ opacity: 1, y: 0 }}
            viewport={{ once: true }}
            transition={{ duration: 0.6 }}
            className="inline-block px-4 py-2 bg-indigo-500/10 border border-indigo-500/20 rounded-full text-indigo-400 text-sm font-semibold mb-6"
          >
            Why Organizations Choose the-M
          </motion.div>

          <motion.h2
            initial={{ opacity: 0, y: 20 }}
            whileInView={{ opacity: 1, y: 0 }}
            viewport={{ once: true }}
            transition={{ duration: 0.6, delay: 0.1 }}
            className="text-4xl md:text-5xl font-bold mb-6"
          >
            Built for Business Impact
          </motion.h2>

          <motion.p
            initial={{ opacity: 0, y: 20 }}
            whileInView={{ opacity: 1, y: 0 }}
            viewport={{ once: true }}
            transition={{ duration: 0.6, delay: 0.2 }}
            className="text-xl text-gray-400 max-w-3xl mx-auto"
          >
            Every feature designed to get AI products to market faster and keep them running reliably
          </motion.p>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-8">
          {benefits.map((benefit, index) => (
            <div
              key={index}
              ref={(el) => {
                if (el) cardsRef.current[index] = el;
              }}
              className="group"
            >
              <motion.div
                whileHover={{ y: -8, scale: 1.02 }}
                transition={{ duration: 0.3 }}
                className="h-full p-8 rounded-2xl bg-white/5 backdrop-blur-sm border border-white/10 hover:border-white/20 transition-colors relative overflow-hidden"
              >
                {/* Gradient background on hover */}
                <div className={`absolute inset-0 bg-gradient-to-br ${benefit.color} opacity-0 group-hover:opacity-10 transition-opacity duration-300`} />

                <div className="relative z-10">
                  <div className="text-6xl mb-4">{benefit.icon}</div>
                  <h3 className="text-2xl font-bold mb-2">{benefit.title}</h3>
                  <div className={`inline-block px-3 py-1 rounded-full bg-gradient-to-r ${benefit.color} text-white text-sm font-semibold mb-4`}>
                    {benefit.stat}
                  </div>
                  <p className="text-gray-400 leading-relaxed text-lg">
                    {benefit.description}
                  </p>
                </div>
              </motion.div>
            </div>
          ))}
        </div>

        {/* CTA */}
        <motion.div
          initial={{ opacity: 0, y: 20 }}
          whileInView={{ opacity: 1, y: 0 }}
          viewport={{ once: true }}
          transition={{ duration: 0.6, delay: 0.4 }}
          className="text-center mt-16"
        >
          <p className="text-gray-400 mb-6">
            Stop fighting infrastructure. Start shipping AI products.
          </p>
          <motion.a
            href="https://aviciot.github.io/theM/"
            target="_blank"
            rel="noopener noreferrer"
            whileHover={{ scale: 1.05 }}
            whileTap={{ scale: 0.95 }}
            className="inline-block px-8 py-4 bg-gradient-to-r from-indigo-600 to-purple-600 text-white rounded-lg font-semibold text-lg hover:from-indigo-700 hover:to-purple-700 transition-colors shadow-lg shadow-indigo-500/50"
          >
            Read Documentation →
          </motion.a>
        </motion.div>
      </div>
    </section>
  );
}
