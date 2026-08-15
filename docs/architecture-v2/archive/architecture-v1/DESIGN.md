---
name: Synthetic Intelligence Hub
colors:
  surface: '#051424'
  surface-dim: '#051424'
  surface-bright: '#2c3a4c'
  surface-container-lowest: '#010f1f'
  surface-container-low: '#0d1c2d'
  surface-container: '#122131'
  surface-container-high: '#1c2b3c'
  surface-container-highest: '#273647'
  on-surface: '#d4e4fa'
  on-surface-variant: '#b9cacb'
  inverse-surface: '#d4e4fa'
  inverse-on-surface: '#233143'
  outline: '#849495'
  outline-variant: '#3b494b'
  surface-tint: '#00dbe9'
  primary: '#dbfcff'
  on-primary: '#00363a'
  primary-container: '#00f0ff'
  on-primary-container: '#006970'
  inverse-primary: '#006970'
  secondary: '#d0bcff'
  on-secondary: '#3c0091'
  secondary-container: '#571bc1'
  on-secondary-container: '#c4abff'
  tertiary: '#f5f5ff'
  on-tertiary: '#283044'
  tertiary-container: '#d1d9f3'
  on-tertiary-container: '#575e75'
  error: '#ffb4ab'
  on-error: '#690005'
  error-container: '#93000a'
  on-error-container: '#ffdad6'
  primary-fixed: '#7df4ff'
  primary-fixed-dim: '#00dbe9'
  on-primary-fixed: '#002022'
  on-primary-fixed-variant: '#004f54'
  secondary-fixed: '#e9ddff'
  secondary-fixed-dim: '#d0bcff'
  on-secondary-fixed: '#23005c'
  on-secondary-fixed-variant: '#5516be'
  tertiary-fixed: '#dae2fd'
  tertiary-fixed-dim: '#bec6e0'
  on-tertiary-fixed: '#131b2e'
  on-tertiary-fixed-variant: '#3f465c'
  background: '#051424'
  on-background: '#d4e4fa'
  surface-variant: '#273647'
typography:
  display:
    fontFamily: Geist
    fontSize: 48px
    fontWeight: '700'
    lineHeight: 56px
    letterSpacing: -0.02em
  headline-lg:
    fontFamily: Geist
    fontSize: 32px
    fontWeight: '600'
    lineHeight: 40px
  headline-md:
    fontFamily: Geist
    fontSize: 24px
    fontWeight: '600'
    lineHeight: 32px
  body-lg:
    fontFamily: Inter
    fontSize: 18px
    fontWeight: '400'
    lineHeight: 28px
  body-md:
    fontFamily: Inter
    fontSize: 16px
    fontWeight: '400'
    lineHeight: 24px
  body-sm:
    fontFamily: Inter
    fontSize: 14px
    fontWeight: '400'
    lineHeight: 20px
  code-label:
    fontFamily: JetBrains Mono
    fontSize: 12px
    fontWeight: '500'
    lineHeight: 16px
    letterSpacing: 0.05em
rounded:
  sm: 0.125rem
  DEFAULT: 0.25rem
  md: 0.375rem
  lg: 0.5rem
  xl: 0.75rem
  full: 9999px
spacing:
  base: 4px
  xs: 4px
  sm: 8px
  md: 16px
  lg: 24px
  xl: 32px
  container-max: 1440px
  gutter: 20px
---

## Brand & Style

The design system is engineered for the next generation of AI orchestration, embodying a "Cyber-Sophisticate" aesthetic. It targets developers and system architects who require a high-density, high-performance interface that feels both powerful and precise.

The visual language balances **Minimalism** with **Glassmorphism**. By using deep, layered backgrounds and vibrant accents, we evoke a sense of digital depth and "intelligence." The interface should feel like a command deck—authoritative, dark, and glowing with live data. High-contrast neon highlights are reserved for active states and AI-driven insights, ensuring the platform feels alive and responsive.

## Colors

The color palette is built on a foundation of deep, atmospheric shadows punctuated by luminous energy.

- **Primary (Electric Cyan):** Used for primary actions, focus states, and the core "AI signature."
- **Secondary (Neon Purple):** Utilized for secondary highlights, specialized agent categories, and subtle data visualizations.
- **Background Tiers:** The UI uses a "Void" sequence: `#020617` (Deepest Navy) for the main canvas and `#0F172A` (Charcoal Navy) for surface containers.
- **Semantic Accents:** Use a vibrant emerald for "Enabled" statuses and a high-energy coral for "Alerts" or "Delete" actions to maintain immediate legibility against the dark backdrop.

## Typography

This design system utilizes a trio of fonts to distinguish between high-level brand moments, functional data, and technical metadata.

1.  **Geist:** Used for headlines and page titles to provide a sharp, technical, yet premium feel.
2.  **Inter:** The workhorse for all body copy and descriptions, ensuring maximum readability in dark mode.
3.  **JetBrains Mono:** Employed for slugs, endpoints, and technical labels to reinforce the "developer-first" nature of the platform.

All text on dark backgrounds should be slightly tracked out (+1-2%) to improve legibility and prevent "haloing" effects.

## Layout & Spacing

The layout follows a **Fixed Grid** philosophy for dashboard views to maintain structural integrity across complex data sets. 

- **Sidebar:** Fixed at 280px to accommodate deep navigation nesting.
- **Main Content:** Centered within a 1440px container.
- **Data Tables:** Use a 40px row height for high-density views, expanding to 64px for detailed agent rows.
- **Mobile Adaption:** On screens <768px, the sidebar collapses into a bottom navigation bar or a hamburger menu. The agent grid reflows from a horizontal table into a vertical stack of "Glass" cards.

## Elevation & Depth

Visual hierarchy is achieved through **Glassmorphism** and **Tonal Layering**.

- **Level 0 (Canvas):** Deep Navy (`#020617`).
- **Level 1 (Panels):** Charcoal Navy (`#0F172A`) with a subtle 1px border (`#1E293B`).
- **Level 2 (Active Cards/Modals):** Semi-transparent surfaces (Opacity: 80%) with a `backdrop-blur` of 12px. These elements feature a "Cyan Glow" shadow: a 0px 4px 20px shadow with 10% opacity of the primary color.
- **Interaction:** Hovering over a list item increases its backdrop opacity and triggers a 1px primary-colored "Inner Glow" border.

## Shapes

The shape language is "Crisp-Tech." We avoid overly organic or round shapes to maintain a professional, high-performance character.

- **Buttons & Inputs:** `0.25rem` (4px) corner radius for a sharp, precise look.
- **Large Containers/Cards:** `0.5rem` (8px) corner radius.
- **Status Chips:** Pill-shaped (fully rounded) to contrast against the rigid grid of the dashboard.

## Components

### Buttons
- **Primary:** Solid Cyan background with black text for maximum contrast. No shadow, but a subtle external glow on hover.
- **Ghost:** 1px border in Cyan with transparent background. Cyan text.
- **Action Icons:** Transparent background, icon color matches text hierarchy (e.g., secondary or neutral).

### Cards & List Items
Agent rows are treated as horizontal cards. They feature a `0.5px` border in a muted slate color. On hover, the border transitions to a Primary Cyan glow.

### Input Fields
Darker than the container background to create an "inset" feel. Borders are invisible until focused, at which point they glow with the Primary color.

### Status Chips
Use "Glow" styling. For "Enabled," use a dark green background with a bright green text and a soft green outer glow. This makes critical status indicators pop against the dark UI.

### AI Indicators
Any element processed or powered by AI should feature a subtle "Secondary" (Purple) to "Primary" (Cyan) gradient border or icon accent.