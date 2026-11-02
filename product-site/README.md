# the-M Product Site

Modern, minimal product/marketing site for the-M platform.

## Tech Stack

- **Next.js 16.2** — Latest version with App Router
- **TypeScript** — Type safety
- **Tailwind CSS** — Utility-first styling
- **GSAP ScrollTrigger** — Scroll-driven animations
- **Framer Motion** — Component animations
- **Vercel** — Deployment (recommended)

## Development

```bash
# Install dependencies
npm install

# Run dev server (port 3001)
npm run dev

# Build for production
npm run build
```

Open [http://localhost:3001](http://localhost:3001)

## Adding Your Demo Video

1. Place your video file in `public/video/demo.mp4`
2. Open `app/components/Hero.tsx`
3. Uncomment the `<video>` element (line ~120)
4. Remove the placeholder `<div>`

## Deployment to Vercel

```bash
# Install Vercel CLI
npm install -g vercel

# Deploy
cd product-site
vercel --prod
```

### Custom Domain

```bash
vercel domains add the-m.io
vercel domains add www.the-m.io
```

## Project Structure

```
product-site/
├── app/
│   ├── components/
│   │   ├── Hero.tsx          # Hero with parallax & video
│   │   └── Features.tsx      # Feature cards with scroll animations
│   ├── layout.tsx            # Root layout
│   ├── page.tsx              # Homepage
│   └── globals.css           # Tailwind + custom CSS
├── public/
│   ├── video/                # Demo videos (add demo.mp4 here)
│   └── images/               # Screenshots, diagrams
├── package.json
└── README.md
```

## Features

✅ **Modern Design** — Minimal, clean, lots of whitespace  
✅ **Scroll Animations** — GSAP ScrollTrigger for reveals  
✅ **Smooth Transitions** — Framer Motion hover effects  
✅ **Dark Mode** — Enabled by default  
✅ **Responsive** — Mobile-first design  
✅ **Fast** — Static export, no server needed  

## Next Steps

1. **Add your video** → `public/video/demo.mp4`
2. **Add more sections** → Use Cases, How It Works, etc.
3. **Add screenshots** → Agent Canvas, Dashboard
4. **Deploy to Vercel** → Get live URL
5. **Custom domain** → `the-m.io` or `product.the-m.io`

## Animation Examples

### Scroll Reveal (GSAP)
Components fade in and slide up as you scroll down.

### Parallax Background (GSAP)
Hero background moves slower than content (depth effect).

### Hover Effects (Framer Motion)
Cards lift up and buttons scale on hover.

## Performance

- Static export (no server needed)
- Optimized CSS (Tailwind purges unused)
- Lazy load images below fold
- Smooth 60fps animations

## Links

- **Technical Docs:** https://aviciot.github.io/theM/
- **GitHub:** https://github.com/aviciot/theM
- **Product Site:** (will be your Vercel URL)
