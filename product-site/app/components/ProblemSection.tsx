'use client';

import { useEffect, useRef } from 'react';
import { motion } from 'framer-motion';
import { gsap } from 'gsap';
import { ScrollTrigger } from 'gsap/ScrollTrigger';

gsap.registerPlugin(ScrollTrigger);

const problems = [
  {
    emoji: '⏱️',
    title: 'Development Takes Forever',
    description: 'Building AI workflows from scratch requires months of engineering time.',
  },
  {
    emoji: '💸',
    title: 'Costs Spiral Out of Control',
    description: 'No visibility into AI costs. Bills arrive as surprises every month.',
  },
  {
    emoji: '🔒',
    title: 'Security Concerns',
    description: 'Sharing AI systems between customers creates data isolation nightmares.',
  },
  {
    emoji: '🔧',
    title: 'Maintenance Burden',
    description: 'Every change requires code deployment. Iterating is painfully slow.',
  },
];

export function ProblemSection() {
  const sectionRef = useRef<HTMLElement>(null);
  const cardsRef = useRef<HTMLDivElement[]>([]);

  useEffect(() => {
    if (!sectionRef.current) return;

    cardsRef.current.forEach((card, index) => {
      if (!card) return;

      gsap.fromTo(
        card,
        { opacity: 0, x: -30 },
        {
          opacity: 1,
          x: 0,
          duration: 0.6,
          delay: index * 0.15,
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
    <section ref={sectionRef} className="py-32 px-6 bg-gradient-to-b from-black to-gray-900">
      <div className="max-w-7xl mx-auto">
        <div className="text-center mb-20">
          <motion.div
            initial={{ opacity: 0, y: 20 }}
            whileInView={{ opacity: 1, y: 0 }}
            viewport={{ once: true }}
            transition={{ duration: 0.6 }}
            className="inline-block px-4 py-2 bg-red-500/10 border border-red-500/20 rounded-full text-red-400 text-sm font-semibold mb-6"
          >
            The Problem
          </motion.div>

          <motion.h2
            initial={{ opacity: 0, y: 20 }}
            whileInView={{ opacity: 1, y: 0 }}
            viewport={{ once: true }}
            transition={{ duration: 0.6, delay: 0.1 }}
            className="text-4xl md:text-5xl font-bold mb-6"
          >
            Building AI Products Is Hard
          </motion.h2>

          <motion.p
            initial={{ opacity: 0, y: 20 }}
            whileInView={{ opacity: 1, y: 0 }}
            viewport={{ once: true }}
            transition={{ duration: 0.6, delay: 0.2 }}
            className="text-xl text-gray-400 max-w-3xl mx-auto"
          >
            Organizations struggle to turn AI experiments into production systems.
            Sound familiar?
          </motion.p>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
          {problems.map((problem, index) => (
            <div
              key={index}
              ref={(el) => {
                if (el) cardsRef.current[index] = el;
              }}
            >
              <div className="h-full p-8 rounded-2xl bg-gradient-to-br from-red-500/5 to-orange-500/5 border border-red-500/10">
                <div className="text-5xl mb-4">{problem.emoji}</div>
                <h3 className="text-2xl font-semibold mb-3 text-red-100">
                  {problem.title}
                </h3>
                <p className="text-gray-400 leading-relaxed">{problem.description}</p>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
