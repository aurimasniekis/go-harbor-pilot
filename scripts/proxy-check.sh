#!/usr/bin/env bash
# Checks the reverse proxy examples in deploy/proxies against what coding
# tools need, by running each proxy in Docker:
#
#   1. in front of a stand-in backend (scripts/proxycheck) that answers like
#      the gateway: event-by-event streaming, a 75s stream kept open only by
#      keepalives, an unstreamed reply after 2 minutes, a 20 MiB request and
#      X-Forwarded-For;
#   2. in front of the real demo: the web UI loads, and signing in and out
#      works (Secure session cookie, Origin check against public_url).
#
#   scripts/proxy-check.sh                 # every proxy
#   scripts/proxy-check.sh nginx caddy     # only these
#   QUICK=1 scripts/proxy-check.sh         # skip the two slow checks
#
# Each config is used as shipped, with the hostname, ports and certificate
# paths swapped for local ones. Needs Docker; nothing leaves the machine.
set -euo pipefail
cd "$(dirname "$0")/.."

BACKEND=18080 HTTP=18880 HTTPS=18443
DIR=${PROXIES_DIR:-deploy/proxies} # another directory of configs to test instead
URL="https://localhost:$HTTPS"
ALL=(nginx apache caddy haproxy traefik)
PROXIES=("${@:-${ALL[@]}}")

declare -A IMAGE=(
  [nginx]=nginx:1.29-alpine
  [apache]=httpd:2.4-alpine
  [caddy]=caddy:2-alpine
  [haproxy]=haproxy:3.2-alpine
  [traefik]=traefik:v3
)

WORK=$(mktemp -d -t harbor-pilot-proxy.XXXXXX)
cleanup() {
  docker rm -f hp-proxy-check >/dev/null 2>&1 || true
  kill "${APP:-}" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

make build >/dev/null
go build -o "$WORK/proxycheck" ./scripts/proxycheck

# A throwaway certificate for localhost.
mkdir -p "$WORK/certs"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=localhost -addext subjectAltName=DNS:localhost \
  -keyout "$WORK/certs/key.pem" -out "$WORK/certs/cert.pem" 2>/dev/null
cat "$WORK/certs/cert.pem" "$WORK/certs/key.pem" >"$WORK/certs/combined.pem"
chmod 644 "$WORK"/certs/*

# localize FILE: the shipped config with local hostname, backend and certificates.
localize() {
  sed -e "s/pilot\.example\.com/localhost/g" \
    -e "s#127\.0\.0\.1:8080#127.0.0.1:$BACKEND#g" \
    -e "s#/etc/letsencrypt/live/localhost/fullchain.pem#/certs/cert.pem#" \
    -e "s#/etc/letsencrypt/live/localhost/privkey.pem#/certs/key.pem#" \
    -e "s#/etc/haproxy/certs/pilot.pem#/certs/combined.pem#" "$1"
}

start_proxy() {
  local p=$1
  local d="$WORK/$p"
  mkdir -p "$d"
  local args=(-d --name hp-proxy-check --network host -v "$WORK/certs:/certs:ro")
  case $p in
    nginx)
      localize "$DIR"/nginx.conf |
        sed -e "s/listen 80;/listen $HTTP;/; s/listen \[::\]:80;/listen [::]:$HTTP;/" \
          -e "s/listen 443 ssl;/listen $HTTPS ssl;/; s/listen \[::\]:443 ssl;/listen [::]:$HTTPS ssl;/" >"$d/default.conf"
      args+=(-v "$d/default.conf:/etc/nginx/conf.d/default.conf:ro") ;;
    apache)
      localize "$DIR"/apache.conf | sed -e "s/\*:80>/*:$HTTP>/; s/\*:443>/*:$HTTPS>/" >"$d/harbor-pilot.conf"
      # A minimal server config with the modules the example names, plus
      # mod_deflate compressing streams, as many distribution configs do, so
      # the example's no-gzip is put to the test.
      cat >"$d/httpd.conf" <<EOF
ServerRoot "/usr/local/apache2"
Listen $HTTP
Listen $HTTPS
LoadModule mpm_event_module modules/mod_mpm_event.so
LoadModule authz_core_module modules/mod_authz_core.so
LoadModule unixd_module modules/mod_unixd.so
LoadModule log_config_module modules/mod_log_config.so
LoadModule alias_module modules/mod_alias.so
LoadModule env_module modules/mod_env.so
LoadModule filter_module modules/mod_filter.so
LoadModule deflate_module modules/mod_deflate.so
LoadModule headers_module modules/mod_headers.so
LoadModule socache_shmcb_module modules/mod_socache_shmcb.so
LoadModule ssl_module modules/mod_ssl.so
LoadModule proxy_module modules/mod_proxy.so
LoadModule proxy_http_module modules/mod_proxy_http.so
User daemon
Group daemon
ServerName localhost
ErrorLog /proc/self/fd/2
LogLevel warn
AddOutputFilterByType DEFLATE text/html text/plain application/json text/event-stream
Include /usr/local/apache2/conf/harbor-pilot.conf
EOF
      args+=(-v "$d/httpd.conf:/usr/local/apache2/conf/httpd.conf:ro" -v "$d/harbor-pilot.conf:/usr/local/apache2/conf/harbor-pilot.conf:ro") ;;
    caddy)
      { printf '{\n\tadmin off\n\thttp_port %s\n\thttps_port %s\n}\n\n' "$HTTP" "$HTTPS"; localize "$DIR"/Caddyfile; } >"$d/Caddyfile"
      args+=(-v "$d/Caddyfile:/etc/caddy/Caddyfile:ro") ;;
    haproxy)
      localize "$DIR"/haproxy.cfg | sed -e "s/bind :80$/bind :$HTTP/; s/bind :443 /bind :$HTTPS /" >"$d/haproxy.cfg"
      args+=(-v "$d/haproxy.cfg:/usr/local/etc/haproxy/haproxy.cfg:ro") ;;
    traefik)
      mkdir -p "$d/dynamic"
      # No ACME locally: drop the resolver and serve the certificate from a file.
      localize "$DIR"/traefik/traefik.yml |
        sed -e "s/\":80\"/\":$HTTP\"/; s/\":443\"/\":$HTTPS\"/" -e '/^certificatesResolvers:/,/^providers:/{/^providers:/!d}' >"$d/traefik.yml"
      localize "$DIR"/traefik/dynamic/harbor-pilot.yml | sed -e 's/certResolver: letsencrypt/{}/; s/^\(      tls:\)$/\1/' |
        sed -e '/^      tls:$/{N;s/\n *{}/ {}/}' >"$d/dynamic/harbor-pilot.yml"
      printf 'tls:\n  certificates:\n    - certFile: /certs/cert.pem\n      keyFile: /certs/key.pem\n' >"$d/dynamic/certs.yml"
      args+=(-v "$d/traefik.yml:/etc/traefik/traefik.yml:ro" -v "$d/dynamic:/etc/traefik/dynamic:ro") ;;
    *) echo "unknown proxy: $p (known: ${ALL[*]})" >&2; exit 64 ;;
  esac
  docker rm -f hp-proxy-check >/dev/null 2>&1 || true
  docker run "${args[@]}" "${IMAGE[$p]}" >/dev/null
  for _ in $(seq 1 60); do
    (exec 3<>"/dev/tcp/127.0.0.1/$HTTPS") 2>/dev/null && return 0
    sleep 0.5
  done
  echo "$p did not start:" >&2
  docker logs hp-proxy-check >&2
  return 1
}

version() {
  case $1 in
    nginx) docker exec hp-proxy-check nginx -v 2>&1 ;;
    apache) docker exec hp-proxy-check httpd -v | head -1 ;;
    caddy) docker exec hp-proxy-check caddy version ;;
    haproxy) docker exec hp-proxy-check haproxy -v | head -1 ;;
    traefik) docker exec hp-proxy-check traefik version | head -1 ;;
  esac
}

quick=()
[ -n "${QUICK:-}" ] && quick=(-quick)
failed=()
for p in "${PROXIES[@]}"; do
  docker pull -q "${IMAGE[$p]}" >/dev/null
  start_proxy "$p"
  echo "== $p ($(version "$p" | tr -s ' \t' ' '))"
  ok=1
  "$WORK/proxycheck" -backend "127.0.0.1:$BACKEND" -url "$URL" "${quick[@]}" || ok=0

  data="$WORK/$p/demo"
  mkdir -p "$data"
  HP_PUBLIC_URL=$URL ./bin/harbor-pilot serve --demo --listen "127.0.0.1:$BACKEND" --data-dir "$data" >"$data/server.log" 2>&1 &
  APP=$!
  for _ in $(seq 1 60); do
    curl -fsS "http://127.0.0.1:$BACKEND/readyz" >/dev/null 2>&1 && break
    sleep 0.5
  done
  "$WORK/proxycheck" -app -url "$URL" || ok=0
  kill "$APP" && wait "$APP" 2>/dev/null || true
  APP=

  [ $ok = 1 ] || { failed+=("$p"); docker logs --tail 20 hp-proxy-check 2>&1 | sed 's/^/    /'; }
  docker rm -f hp-proxy-check >/dev/null
done

if [ ${#failed[@]} -gt 0 ]; then
  echo "failed: ${failed[*]}"
  exit 1
fi
echo "all passed: ${PROXIES[*]}"
