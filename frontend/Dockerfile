FROM node:20-alpine AS base
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci --prefer-offline

FROM base AS builder
COPY . .
# Ensure Material Symbols font is present (committed file used; wget as fallback)
RUN if [ ! -f public/fonts/material-symbols-outlined.ttf ]; then \
      mkdir -p public/fonts && \
      wget -q "https://fonts.gstatic.com/s/materialsymbolsoutlined/v369/kJF1BvYX7BgnkSrUwT8OhrdQw4oELdPIeeII9v6oDMzByHX9rA6RzaxHMPdY43zj-jCxv3fzvRNU22ZXGJpEpjC_1v-p_4MrImHCIJIZrDCvHOem.ttf" \
      -O public/fonts/material-symbols-outlined.ttf; \
    fi
RUN npm run build

FROM node:20-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
COPY --from=builder /app/.next/standalone ./
COPY --from=builder /app/.next/static ./.next/static
COPY --from=builder /app/public ./public
EXPOSE 3200
CMD ["node", "server.js"]
