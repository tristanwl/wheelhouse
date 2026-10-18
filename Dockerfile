FROM node:20-alpine AS deps
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install

FROM node:20-alpine AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
RUN npm run build

FROM node:20-alpine AS run
WORKDIR /app
ENV NODE_ENV=production
# Bind all interfaces. Without this the Next.js standalone server uses the
# container's hostname, which under host networking can resolve to a single
# interface (e.g. a Tailscale address).
ENV HOSTNAME=0.0.0.0
# ffmpeg/ffprobe power the Codec Analysis "Convert" feature
# (lib/convert-queue.ts). libva-intel-driver (i965, for 4th-gen/
# Haswell Intel iGPUs) and mesa-va-gallium (broader VAAPI fallback) enable Intel
# Quick Sync hardware H.264 encode via /dev/dri (lib/hw-encode.ts) — the
# container falls back to software x264 if neither actually works on the
# host, detected live by a real test encode rather than assumed.
RUN apk add --no-cache ffmpeg libva-intel-driver mesa-va-gallium
COPY --from=build /app/.next/standalone ./
COPY --from=build /app/.next/static ./.next/static
COPY --from=build /app/public ./public
EXPOSE 3050
ENV PORT=3050
CMD ["node", "server.js"]
