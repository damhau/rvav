# The web app, built for the real API and served by nginx, which also
# proxies /api to the rvav API so the browser talks to one origin.
FROM node:22-bookworm-slim AS build
WORKDIR /app
RUN corepack enable
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
# Vite reads these at build time: rebuild the image to change them.
ARG VITE_AUTH_MODE=oidc
ARG VITE_OIDC_AUTHORITY=
ARG VITE_OIDC_CLIENT_ID=rvav
ENV VITE_API_MODE=real \
    VITE_AUTH_MODE=${VITE_AUTH_MODE} \
    VITE_OIDC_AUTHORITY=${VITE_OIDC_AUTHORITY} \
    VITE_OIDC_CLIENT_ID=${VITE_OIDC_CLIENT_ID}
RUN pnpm build

FROM nginx:1.27-alpine
# The image renders templates with envsubst at start-up (RVAV_API_UPSTREAM,
# RVAV_DNS_RESOLVER: Docker's DNS by default; the cluster DNS on Kubernetes).
COPY nginx/default.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /app/dist /usr/share/nginx/html
ENV RVAV_API_UPSTREAM=http://api:8080 \
    RVAV_DNS_RESOLVER=127.0.0.11
EXPOSE 8080
