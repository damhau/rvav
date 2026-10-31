#!/bin/sh
# Checks the web image's nginx configuration in a real nginx (ADR-0030):
# the page's Content-Security-Policy lets the browser reach the identity
# provider's origins the API says, and the one RVAV_WEB_OIDC_AUTHORITY sets;
# a header that isn't a list of origins is dropped; when the API is down the
# page is still served, without them. Runs the stock nginx image with the
# template, a stand-in page and a stand-in API (nginx too): no build.
#
#   web/nginx/check.sh            (Docker; CI's nginx job)
set -eu

here=$(cd "$(dirname "$0")" && pwd)
image=nginx:1.30-alpine
run=rvav-nginx-check-$$
net=$run
work=$(mktemp -d)

cleanup() {
	docker rm -f "$run-web" "$run-api" >/dev/null 2>&1 || true
	docker network rm "$net" >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

fail() {
	echo "FAIL: $*" >&2
	echo "--- nginx's log" >&2
	docker logs "$run-web" >&2 2>&1 || true
	exit 1
}

mkdir -p "$work/html/assets" "$work/api"
echo '<!doctype html><title>rvav</title>' >"$work/html/index.html"
echo 'console.log(1)' >"$work/html/assets/app.js"
chmod -R a+rX "$work"

# A stand-in API: GET /api/v1/auth/options with the header given.
api() {
	docker rm -f "$run-api" >/dev/null 2>&1 || true
	cat >"$work/api/default.conf" <<EOF
server {
    listen 8080;
    location = /api/v1/auth/options {
        add_header X-Rvav-Idp-Origins "$1" always;
        default_type application/json;
        return 200 '{}';
    }
    location / { return 404; }
}
EOF
	docker run -d --name "$run-api" --network "$net" --network-alias api \
		-v "$work/api/default.conf:/etc/nginx/conf.d/default.conf:ro" "$image" >/dev/null
	for _ in 1 2 3 4 5 6 7 8 9 10; do
		docker exec "$run-api" wget -qO /dev/null http://127.0.0.1:8080/api/v1/auth/options 2>/dev/null && return 0
		sleep 0.5
	done
	fail "the stand-in API didn't start"
}

# The response headers of a path of the web container.
headers() {
	docker exec "$run-web" wget -S -O /dev/null "http://127.0.0.1:8080$1" 2>&1 || true
}

csp() {
	headers "$1" | sed -n 's/^ *Content-Security-Policy: //p'
}

expect_csp() { # path, what connect-src and frame-src hold after 'self'
	got=$(csp "$1")
	case "$got" in
	*"connect-src 'self' $2;"*"frame-src 'self' $2;"*) echo "ok: $1: $2" ;;
	*) fail "$1: expected connect-src and frame-src 'self' $2, got: $got" ;;
	esac
}

docker network create "$net" >/dev/null
docker run -d --name "$run-web" --network "$net" \
	-e RVAV_API_UPSTREAM=http://api:8080 \
	-e NGINX_ENTRYPOINT_LOCAL_RESOLVERS=1 \
	-e RVAV_WEB_AUTH_MODE=oidc \
	-e RVAV_WEB_OIDC_AUTHORITY=https://web.example/realms/rvav \
	-e RVAV_WEB_OIDC_CLIENT_ID= \
	-e RVAV_WEB_OIDC_SCOPE= \
	-e RVAV_WEB_SCRIPT_SOURCES= \
	-v "$here/default.conf.template:/etc/nginx/templates/default.conf.template:ro" \
	-v "$work/html:/usr/share/nginx/html:ro" "$image" >/dev/null
for _ in 1 2 3 4 5 6 7 8 9 10; do
	headers /config.js | grep -q 'HTTP/1.1 200' && break
	sleep 0.5
done
docker exec "$run-web" nginx -t >/dev/null 2>&1 || fail "nginx -t"

# The API isn't there: the page is served, with the web container's origin only.
headers /deployments | grep -q 'HTTP/1.1 200' || fail "the page isn't served while the API is down"
expect_csp /deployments "https://web.example "

# The API says the provider's origin and one being tried.
api "https://login.microsoftonline.com https://idp.corp.example:8443"
expect_csp /auth/trial "https://web.example https://login.microsoftonline.com https://idp.corp.example:8443"
expect_csp / "https://web.example https://login.microsoftonline.com https://idp.corp.example:8443"
expect_csp /index.html "https://web.example https://login.microsoftonline.com https://idp.corp.example:8443"
headers /index.html | grep -qi 'Cache-Control: no-cache' || fail "the page may be cached"
# Files and the API don't ask it.
expect_csp /assets/app.js "https://web.example "
docker exec "$run-web" wget -qO- http://127.0.0.1:8080/config.js | grep -q '"oidcClientId": ""' ||
	fail "/config.js"

# A header that isn't a list of origins is dropped.
api "https://idp.example; script-src *"
expect_csp /deployments "https://web.example "

echo "nginx: the Content-Security-Policy follows the API's identity provider"
