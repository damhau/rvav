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
ARG VITE_OIDC_SCOPE="openid profile email"
ENV VITE_API_MODE=real \
    VITE_AUTH_MODE=${VITE_AUTH_MODE} \
    VITE_OIDC_AUTHORITY=${VITE_OIDC_AUTHORITY} \
    VITE_OIDC_CLIENT_ID=${VITE_OIDC_CLIENT_ID} \
    VITE_OIDC_SCOPE=${VITE_OIDC_SCOPE}
RUN pnpm build

FROM nginx:1.30-alpine
# The base's packages with their latest fixes (S14.6: the image is scanned).
RUN apk upgrade --no-cache
ARG VITE_AUTH_MODE=oidc
ARG VITE_OIDC_AUTHORITY=
ARG VITE_OIDC_CLIENT_ID=rvav
ARG VITE_OIDC_SCOPE="openid profile email"
# The image renders templates with envsubst at start-up (RVAV_API_UPSTREAM),
# and NGINX_ENTRYPOINT_LOCAL_RESOLVERS has it read the nameservers from
# /etc/resolv.conf: Docker's DNS, or the cluster DNS on Kubernetes.
COPY nginx/default.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /app/dist /usr/share/nginx/html
# The sign-in settings at run time (/config.js), the build's by default; the
# origins of the console's SDK scripts, for the Content-Security-Policy.
ENV RVAV_API_UPSTREAM=http://api:8080 \
    NGINX_ENTRYPOINT_LOCAL_RESOLVERS=1 \
    RVAV_WEB_AUTH_MODE=${VITE_AUTH_MODE} \
    RVAV_WEB_OIDC_AUTHORITY=${VITE_OIDC_AUTHORITY} \
    RVAV_WEB_OIDC_CLIENT_ID=${VITE_OIDC_CLIENT_ID} \
    RVAV_WEB_OIDC_SCOPE=${VITE_OIDC_SCOPE} \
    RVAV_WEB_SCRIPT_SOURCES=
EXPOSE 8080
