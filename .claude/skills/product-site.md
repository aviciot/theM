---
name: product-site
description: Build modern, high-end product/marketing sites with animations
tags: [frontend, design, animations, product]
---

# High-End Product Site Builder Skill

Build modern, minimal, high-end product sites with scroll-driven animations.

## Tech Stack (2026)

- **Next.js 16.2** — Latest, App Router
- **TypeScript** — Type safety
- **Tailwind CSS** — Utility-first styling
- **GSAP ScrollTrigger** — Scroll animations (parallax, reveals)
- **Framer Motion** — Component animations (transitions, hover)
- **Vercel** — Deployment

## Design Principles

1. **Minimal & Clean** — Lots of whitespace, clear hierarchy
2. **Subtle Animations** — Smooth, purposeful, not flashy
3. **Scroll-Driven** — Content reveals as you scroll (2026 trend #1)
4. **Fast & Performant** — No bloat, optimized images/video
5. **Dark Mode** — Modern expectation

## Animation Guidelines

### GSAP ScrollTrigger — Use for:
- Hero section parallax
- Section fade-ins on scroll
- Pinned scroll sections
- Timeline sequences
- Number counters

### Framer Motion — Use for:
- Page transitions
- Hover effects
- Button animations
- Modal open/close
- State-driven UI changes

### Avoid:
- Auto-playing carousels
- Excessive parallax (causes motion sickness)
- Heavy 3D unless critical
- Animations longer than 0.6s

## Section Structure (Typical Product Site)

```
1. Hero
   - Bold headline
   - Subheadline (1 sentence)
   - Embedded video or animated graphic
   - CTA button
   - Scroll indicator

2. Problem Statement
   - Pain points (3-4 bullets)
   - Visual diagram

3. Solution (the-M Features)
   - Feature cards with icons
   - Scroll-triggered reveals
   - Screenshots/GIFs

4. How It Works
   - 3-5 steps with numbers
   - Visual flow diagram
   - Code example or demo

5. Use Cases
   - 3-4 real scenarios
   - Before/After comparisons

6. Technical Overview
   - Architecture diagram
   - Link to technical docs

7. Social Proof (if applicable)
   - Testimonials
   - Logos
   - Metrics

8. CTA Section
   - Get Started button
   - Link to docs
   - GitHub link

9. Footer
   - Links (Docs, GitHub, Support)
   - Social icons
```

## File Structure

```
product-site/
├── app/
│   ├── page.tsx              # Homepage
│   ├── layout.tsx            # Root layout (fonts, metadata)
│   ├── globals.css           # Tailwind + custom CSS
│   └── components/
│       ├── Hero.tsx          # Hero section
│       ├── Features.tsx      # Feature cards
│       ├── HowItWorks.tsx    # Steps diagram
│       ├── UseCases.tsx      # Use case cards
│       ├── Footer.tsx        # Footer
│       └── ScrollReveal.tsx  # Reusable scroll animation wrapper
├── public/
│   ├── video/                # Demo videos
│   ├── images/               # Screenshots, diagrams
│   └── icons/                # Feature icons
├── lib/
│   └── animations.ts         # Reusable animation configs
├── package.json
├── tailwind.config.js
├── tsconfig.json
└── next.config.js
```

## Color Palette (Example for SaaS/AI)

```css
/* Minimal, modern */
--bg-primary: #ffffff;
--bg-secondary: #f8f9fa;
--text-primary: #0a0a0a;
--text-secondary: #6b7280;
--accent: #4f46e5; /* Indigo */
--accent-hover: #4338ca;

/* Dark mode */
--bg-primary-dark: #0a0a0a;
--bg-secondary-dark: #1a1a1a;
--text-primary-dark: #ffffff;
--text-secondary-dark: #9ca3af;
```

## Typography

```
Headings: Inter, SF Pro Display, or Geist (Vercel's font)
Body: Inter or System UI
Code: JetBrains Mono or Fira Code
```

## Animation Timings

```
Fast: 0.2s (hover, click feedback)
Default: 0.3-0.4s (transitions, fades)
Slow: 0.6s (page transitions, large movements)
Ease: cubic-bezier(0.4, 0.0, 0.2, 1) — Material Design standard
```

## Video Embedding

```tsx
// For user's animation video
<video
  autoPlay
  loop
  muted
  playsInline
  className="w-full rounded-lg shadow-2xl"
>
  <source src="/video/demo.mp4" type="video/mp4" />
</video>
```

## Performance Checklist

- [ ] Lazy load images below fold
- [ ] Use Next.js Image component
- [ ] Optimize video (H.264, < 10MB)
- [ ] Preload hero assets
- [ ] Remove unused Tailwind classes (production)
- [ ] Enable GSAP's `scrub` for smooth scroll
- [ ] Test on mobile (60fps target)

## Deployment (Vercel)

```bash
npm install -g vercel
cd product-site
vercel --prod
```

Custom domain: `vercel domains add the-m.io`

## Example Inspirations (2026 Trends)

- **Linear.app** — Minimal, smooth scroll animations
- **Vercel.com** — Clean, fast, dark mode
- **Stripe.com** — Subtle animations, great diagrams
- **Loom.com** — Video-first, clear value prop
- **Notion.so** — Simple, effective, not over-animated

## Anti-Patterns (Avoid)

- ❌ Stock photos of people pointing at laptops
- ❌ Generic "innovative solution" copy
- ❌ Walls of text without visuals
- ❌ Auto-playing sound
- ❌ Pop-ups on first visit
- ❌ Slow-loading hero videos (>10MB)
- ❌ Animations that block interaction

## When to Use What

| Need | Tool |
|------|------|
| Scroll-triggered section reveals | GSAP ScrollTrigger |
| Parallax hero background | GSAP ScrollTrigger |
| Button hover glow | Framer Motion |
| Page transitions | Framer Motion |
| Number counter on scroll | GSAP + ScrollTrigger |
| Modal open/close | Framer Motion |
| Pinned scroll section | GSAP ScrollTrigger |
| Drag interactions | Framer Motion |
| Timeline-based sequences | GSAP Timeline |
| React state animations | Framer Motion |

## Code Snippets

### GSAP Scroll Reveal (Fade In)
```tsx
'use client';
import { useEffect, useRef } from 'react';
import { gsap } from 'gsap';
import { ScrollTrigger } from 'gsap/ScrollTrigger';

gsap.registerPlugin(ScrollTrigger);

export function ScrollReveal({ children }: { children: React.ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!ref.current) return;
    gsap.fromTo(
      ref.current,
      { opacity: 0, y: 50 },
      {
        opacity: 1,
        y: 0,
        duration: 0.8,
        ease: 'power2.out',
        scrollTrigger: {
          trigger: ref.current,
          start: 'top 80%',
        },
      }
    );
  }, []);

  return <div ref={ref}>{children}</div>;
}
```

### Framer Motion Button Hover
```tsx
'use client';
import { motion } from 'framer-motion';

export function CTAButton({ children }: { children: React.ReactNode }) {
  return (
    <motion.button
      whileHover={{ scale: 1.05, boxShadow: '0 10px 30px rgba(79, 70, 229, 0.3)' }}
      whileTap={{ scale: 0.95 }}
      className="px-8 py-4 bg-indigo-600 text-white rounded-lg font-semibold"
    >
      {children}
    </motion.button>
  );
}
```

### Hero Parallax
```tsx
'use client';
import { useEffect, useRef } from 'react';
import { gsap } from 'gsap';
import { ScrollTrigger } from 'gsap/ScrollTrigger';

gsap.registerPlugin(ScrollTrigger);

export function Hero() {
  const bgRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!bgRef.current) return;
    gsap.to(bgRef.current, {
      y: 200,
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
    <section className="relative h-screen overflow-hidden">
      <div ref={bgRef} className="absolute inset-0 bg-gradient-to-br from-indigo-500 to-purple-600" />
      <div className="relative z-10 flex items-center justify-center h-full">
        <h1 className="text-6xl font-bold text-white">the-M</h1>
      </div>
    </section>
  );
}
```

## Next Steps After Building

1. Set up Vercel deployment
2. Add custom domain
3. Enable analytics (Vercel Analytics or Plausible)
4. Test on mobile/tablet
5. Run Lighthouse audit (aim for 90+ performance)
6. Set up OG image for social sharing
7. Add sitemap.xml for SEO
