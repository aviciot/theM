# Product Site - Docker Setup

## Quick Start (Test in Docker)

### 1. Build and Start

```bash
cd /opt/docker/them

# Start the product site container
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml up -d them-product-site

# Check logs
docker logs them-product-site -f
```

### 2. Access

Open in browser: **http://product.localhost**

(Traefik routes `product.localhost` → product site container)

### 3. Hot Reload

The container has live reload enabled. Edit files in `product-site/` and see changes immediately.

### 4. Stop

```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml stop them-product-site
```

---

## Adding Your Video

1. Place video at: `product-site/public/video/demo.mp4`
2. Edit `product-site/app/components/Hero.tsx` (line ~120)
3. Uncomment the `<video>` element
4. Container will auto-reload

---

## Full Stack (with Backend)

If you want to test with the full the-M stack:

```bash
# Start everything (including product site)
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d

# Access:
# - Product site: http://product.localhost
# - Dashboard: http://localhost:3000
# - API: http://localhost:8081
```

---

## Deploy to Vercel (Later)

When ready to deploy:

1. **Change next.config.ts:**
   ```typescript
   output: "export",  // Change from "standalone"
   ```

2. **Deploy:**
   ```bash
   cd product-site
   npm install -g vercel
   vercel --prod
   ```

3. **Custom domain:**
   ```bash
   vercel domains add the-m.io
   ```

---

## Troubleshooting

### Container won't start
```bash
# Check if port 3000 is in use
docker ps | grep 3000

# Rebuild
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml up -d --build them-product-site
```

### Can't access product.localhost
```bash
# Check Traefik routes
docker logs them-traefik | grep product

# Make sure Traefik is running
docker ps | grep traefik
```

### Changes not showing
```bash
# Hard restart
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml restart them-product-site
```

---

## File Structure

```
product-site/
├── Dockerfile              # Production build
├── Dockerfile.dev          # Development (hot reload)
├── app/
│   ├── components/
│   │   ├── Hero.tsx        # Hero with video
│   │   └── Features.tsx    # Feature cards
│   ├── page.tsx            # Homepage
│   └── layout.tsx
├── public/
│   └── video/              # Put demo.mp4 here
└── package.json
```
