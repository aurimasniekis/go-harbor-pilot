# Behind a reverse proxy

Run Harbor Pilot on the loopback address and put a reverse proxy in front of it to terminate TLS. The proxy has to
cope with traffic that doesn't look like a website: coding tools stream replies for minutes at a time, send the whole
conversation with every request, and sometimes wait a long time for a reply that is not streamed.

This page has a tested configuration for [nginx](#nginx), [Apache httpd](#apache-httpd), [Caddy](#caddy),
[Traefik](#traefik) and [HAProxy](#haproxy), and notes for [Cloudflare Tunnel](#cloudflare-tunnel) and
[Kubernetes](#other-proxies-and-kubernetes). The files are in
[`deploy/proxies`](https://github.com/aurimasniekis/go-harbor-pilot/tree/main/deploy/proxies).

## What the proxy has to do

| Need | Why | A default that breaks it |
|---|---|---|
| Pass streamed replies on as they are written | Tools show text as it arrives and give up on a stream that stays silent. | None of the five proxies held a stream back, with or without compression; check any other proxy with the script below. |
| Accept request bodies up to 32 MiB | Every request carries the whole conversation, including pasted files and images. | nginx refuses anything over 1 MB with `413 Request Entity Too Large`. |
| Wait minutes for a reply that is not streamed | Some tools and SDK calls ask for the whole reply at once, which arrives when the model finishes. | nginx gives up after 60 seconds with `504 Gateway Timeout`, Apache with `502 Bad Gateway`. |
| Keep a quiet stream open | While a model thinks, the only bytes on a stream are Harbor Pilot's keepalives, one every 15 seconds. | A proxy timeout shorter than 15 seconds between bytes. |
| Forward the client's address | Audit entries, sign-in rate limits and request pages use it. | Without `trusted_proxies`, every client looks like the proxy and they share one sign-in rate limit. |

Harbor Pilot already helps: on a stream it sends response headers within 10 seconds even when the provider is slow,
sends `Cache-Control: no-cache` and `X-Accel-Buffering: no`, and then a keepalive every 15 seconds. WebSockets are not
used, so no `Upgrade` handling is needed.

## Harbor Pilot settings

Whichever proxy you use, tell Harbor Pilot its public address and which proxy to believe:

```yaml
server:
  listen: 127.0.0.1:8080
  public_url: https://pilot.example.com   # HP_PUBLIC_URL
  trusted_proxies: ["127.0.0.1/32"]       # the proxy's address
```

- **`public_url`** must be the `https://` address people and tools use. Session cookies are only marked `Secure`
  under HTTPS, changes from the web UI are only accepted from this origin, and setup links and tool snippets are built
  from it.
- **`trusted_proxies`** lists the proxy's address, so the client address is taken from `X-Forwarded-For`: Harbor Pilot
  walks the header from the right and uses the first address that is not a trusted proxy. When Harbor Pilot runs in
  Docker and the proxy on the host, the proxy connects from the Docker network's gateway, not from `127.0.0.1`: list
  that network instead (for example `["172.16.0.0/12"]`).
- With any `trusted_proxies` set, `/metrics` always needs `server.metrics_token_file`, because every request now
  arrives from the proxy. See [Monitoring](/operators/monitoring).

## nginx

Needs nginx 1.25.1 or later for `http2 on`. Save as `/etc/nginx/conf.d/harbor-pilot.conf`:

<<< @/../deploy/proxies/nginx.conf{nginx}

`client_max_body_size` and `proxy_read_timeout` are the two settings nginx's defaults get wrong. Streaming works without
extra care: Harbor Pilot's `X-Accel-Buffering: no` already tells nginx not to buffer, and `proxy_buffering off` also
stops nginx spooling large replies to disk.

## Apache httpd

Enable the modules first: `a2enmod proxy proxy_http ssl headers`. Save as
`/etc/apache2/sites-available/harbor-pilot.conf` and run `a2ensite harbor-pilot`:

<<< @/../deploy/proxies/apache.conf{apache}

`ProxyTimeout` is the setting Apache's default gets wrong: without it, a reply that is not streamed fails with
`502 Bad Gateway` after 60 seconds. Apache
forwards `X-Forwarded-For` by itself.

## Caddy

Caddy gets and renews the certificate itself, forwards `Host` and `X-Forwarded-For`, has no response timeout and
passes `text/event-stream` on as it arrives:

<<< @/../deploy/proxies/Caddyfile{txt}

Compression with `encode` is fine: Caddy still sends each event on as it arrives.

## Traefik

Traefik v3 with the file provider. The static configuration, `/etc/traefik/traefik.yml`:

<<< @/../deploy/proxies/traefik/traefik.yml{yaml}

The route, `/etc/traefik/dynamic/harbor-pilot.yml`:

<<< @/../deploy/proxies/traefik/dynamic/harbor-pilot.yml{yaml}

Traefik's defaults suit Harbor Pilot: it streams, forwards `X-Forwarded-For` and has no response timeout.
A `compress` middleware is fine too: Traefik still sends each event on as it arrives. With Docker labels instead of the file provider, the same router and service become
`traefik.http.routers.harbor-pilot.*` and `traefik.http.services.harbor-pilot.loadbalancer.server.port=8080` labels on
the Harbor Pilot container.

## HAProxy

HAProxy wants the certificate and key in one file: `cat fullchain.pem privkey.pem > /etc/haproxy/certs/pilot.pem`.

<<< @/../deploy/proxies/haproxy.cfg{txt}

HAProxy streams and has no body size limit; `timeout client` and `timeout server` are the settings that matter. The
health check uses `/healthz`, so HAProxy stops sending traffic while Harbor Pilot restarts.

## Cloudflare Tunnel

`cloudflared` connects out to Cloudflare, so nothing needs to listen publicly: point the tunnel's ingress at
`http://127.0.0.1:8080` and set `public_url` to the tunnel's hostname. Two Cloudflare limits apply (from Cloudflare's
documentation; the check script below does not run against Cloudflare):

- A request that gets no response for 100 seconds fails with error 524. Streamed requests are not affected, because
  Harbor Pilot sends headers within 10 seconds and keepalives every 15, but a long reply that is not streamed is.
  Coding tools stream by default.
- Request bodies are limited by your Cloudflare plan (100 MB on the Free and Pro plans), above Harbor Pilot's 32 MiB.

`cloudflared` itself connects to Harbor Pilot from `127.0.0.1`, so `trusted_proxies: ["127.0.0.1/32"]` applies.

## Other proxies and Kubernetes

Any proxy works if it meets the [five needs above](#what-the-proxy-has-to-do). On Kubernetes, the same settings apply
to whichever ingress controller or gateway you run, with `trusted_proxies` set to its pods' network.

## How these were checked

`scripts/proxy-check.sh` runs each proxy in Docker with the file from `deploy/proxies` (with the hostname, ports and
certificate paths swapped for local ones) and checks it twice:

1. In front of a stand-in backend that answers like the gateway: five events one second apart must arrive one by one
   (with `Accept-Encoding: gzip, deflate, br, zstd` set, as tools send it); a 75-second stream that carries nothing but
   keepalives must stay open; a reply that takes two minutes and is not streamed must arrive; a 20 MiB request must be
   accepted; and `X-Forwarded-For` must reach the backend.
2. In front of the real demo: the web UI loads, and signing in and out works, which needs the `Secure` session cookie
   and the origin check against `public_url`.

```sh
scripts/proxy-check.sh                # every proxy, about 12 minutes
scripts/proxy-check.sh nginx caddy    # only these
QUICK=1 scripts/proxy-check.sh        # skip the 75-second and 2-minute checks
```

To be sure the checks catch problems, they were also run against broken configurations: nginx without
`client_max_body_size` fails with 413, and without `proxy_read_timeout` with 504; Apache without `ProxyTimeout`
fails with 502. Compression turned on for streams (nginx `gzip_types text/event-stream`, Apache
`mod_deflate`, Caddy `encode`, Traefik `compress`) did not hold events back in any of them.

Last run: 2026-11-06, Harbor Pilot at `main`, every check passing:

| Proxy | Version |
|---|---|
| nginx | 1.29.8 |
| Apache httpd | 2.4.69 |
| Caddy | 2.11.9 |
| Traefik | 3.7.17 |
| HAProxy | 3.2.26 |
