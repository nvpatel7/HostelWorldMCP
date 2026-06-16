# hostelworld-mcp

An unofficial, public **Model Context Protocol (MCP)** server for [Hostelworld](https://www.hostelworld.com), written in Go. Lets AI assistants like ChatGPT and Claude search hostels and produce a booking deep-link inside a natural conversation.

> **Status: working against the live site.** No Partner API key ever arrived, so instead of a contracted API the server **scrapes Hostelworld's public PWA JSON backend** on demand — resolving the city, listing properties, and reading room availability the same way the website does. Because that backend is unofficial and can change without notice, every upstream call is wrapped in a **circuit breaker** and conservative rate limits, so when scraping breaks the tools fail fast with a clean error instead of hammering a broken upstream. A **demo mode** with embedded fixtures still runs fully offline for development and tests.
>
> The architectural rationale behind every layer (transport, tools, scraping, circuit breaker, rate limiting, budget cap, error taxonomy) is in [`DESIGN.md`](DESIGN.md). Each design decision is documented with the alternatives considered and why they were rejected.

## What it does

Three tools, registered with the MCP client:

| Tool | Purpose |
| --- | --- |
| `search_hostels` | Find properties by city, dates, guest count. Supports `exclude_ids` so "show me different ones" works without re-showing duplicates. |
| `get_hostel_details` | Rooms, prices, amenities for a specific property. |
| `get_booking_url` | A pre-filled `hostelworld.com` deep link — payment happens on Hostelworld's site; this server never touches it. |

## Quickstart (live scrape, no key needed)

```bash
cp .env.example .env       # HOSTELWORLD_DEMO=false → live scrape
go run ./cmd/hostelworld-mcp
```

The server resolves the city, fetches live properties and prices from Hostelworld's PWA backend, and bootstraps the public api-key automatically from the site (no credentials to configure). It listens on `127.0.0.1:8080`. The MCP endpoint is `POST /mcp` (Streamable HTTP transport). `GET /healthz` is unauthenticated and used for liveness checks.

To exercise it without writing an MCP client, point [`mcp-inspector`](https://github.com/modelcontextprotocol/inspector) at `http://127.0.0.1:8080/mcp` and call the tools interactively.

## Demo mode (offline, no network)

```bash
HOSTELWORLD_DEMO=true go run ./cmd/hostelworld-mcp
```

Serves embedded fixture responses instead of scraping the live site — handy for development and tests without network access.

## How the scrape works

Hostelworld's public site (`www.hostelworld.com/pwa`) is a single-page app backed by a JSON API at `prod.apigee.hostelworld.com`. The server targets that JSON backend directly:

1. **City → id**: `autocomplete-service/v1/autocomplete/web?text=<city>` resolves a city name to Hostelworld's numeric city id (cached 24h).
2. **Search**: `legacy-hwapi-service/2.2/cities/<id>/properties/` returns live properties, ratings, and lowest prices.
3. **Details**: `…/properties/<id>/` (static info, cached 1h) + `…/properties/<id>/availability/` (live rooms/prices, never cached).
4. **api-key**: the public key the PWA ships to browsers is extracted from the page's runtime config and refreshed automatically if Hostelworld rotates it.

Every one of these calls runs through a circuit breaker plus a global rate limiter and an in-flight concurrency cap.

## Configuration

All configuration is via environment variables (see [`.env.example`](.env.example)):

| Variable | Default | Purpose |
| --- | --- | --- |
| `HOSTELWORLD_DEMO` | `false` | Serve embedded fixtures instead of scraping the live site. |
| `HOSTELWORLD_BASE_URL` | `https://prod.apigee.hostelworld.com` | apigee backend host. |
| `HOSTELWORLD_APIGEE_KEY` | — | Optional. Pin the public api-key instead of scraping it from the page. |
| `HOSTELWORLD_USER_AGENT` | browser UA | Optional. Override the request User-Agent. |
| `MAX_IN_FLIGHT` | `4` | Max concurrent upstream requests. |
| `BREAKER_MAX_FAILURES` / `BREAKER_COOLDOWN_SECS` | `5` / `30` | Circuit-breaker trip threshold + open cooldown. |
| `LISTEN_ADDR` | `127.0.0.1:8080` | HTTP listen address. |
| `DAILY_BUDGET` | `10000` | Upstream calls per UTC day before the hard cap. |
| `SOFT_CAP_PCT` / `HARD_CAP_PCT` | `70` / `95` | Thresholds for cache-only / refuse-all states. |
| `RATE_BUCKET` / `RATE_REFILL_PER_SEC` | `20` / `0.2` | Per-IP token bucket sizing. |
| `GLOBAL_QPS` / `GLOBAL_BURST` | `2.0` / `5` | Hard ceiling on upstream calls per second (kept low — unofficial backend). |
| `REAL_IP_HEADER` | — | Set to `CF-Connecting-IP` when behind Cloudflare. |

## Design notes worth surfacing

These details are easy to miss if you only read the code:

- **No auth.** The MCP endpoint is open by design — this is an unofficial public MCP. Protection comes from per-IP rate limits and a daily upstream-call budget cap, not credentials. See [`DESIGN.md` §9](DESIGN.md#9-authentication).
- **No conversation state.** "Show me more without duplicates" is solved by passing `exclude_ids` from the model on each call, not by a server-side session store. See [§6](DESIGN.md#6-conversation-state-the-show-me-more-problem).
- **Availability is not cached.** Static property info and city-name lookups are; prices are not, to avoid showing a price that's stale when the user reaches Hostelworld's checkout. See [§12](DESIGN.md#12-caching).
- **Payment stays on hostelworld.com.** `get_booking_url` returns a deep link to Hostelworld's existing property page. We never touch cards. See [§8](DESIGN.md#8-booking-handoff).
- **The upstream can break.** Because we scrape an unofficial backend, a single circuit breaker guards all upstream calls; when it trips open, tools fail fast with `service_busy` rather than retrying a broken endpoint. See [§11.7](DESIGN.md#117-circuit-breaker).

## Project layout

```
.
├── cmd/hostelworld-mcp/         # main.go — wiring, lifecycle
├── internal/
│   ├── breaker/                 # circuit breaker around the upstream
│   ├── budget/                  # daily upstream-call cap with on-disk persistence
│   ├── cache/                   # TTL + LRU wrapper
│   ├── config/                  # env-loaded config with redacted Stringer
│   ├── errors/                  # taxonomy (codes + safe/internal faces)
│   ├── hostelworld/             # scrape client (apigee mapping) + demo client + api-key bootstrap
│   │   └── fixtures/            # embedded demo JSON + recorded apigee fixtures
│   ├── mcpserver/               # tool registration + handlers + middleware
│   ├── ratelimit/               # per-IP token bucket
│   └── slug/                    # slugifier for booking URLs
├── DESIGN.md                    # architecture decisions + rejected alternatives
└── README.md                    # this file
```

## Tests

```bash
go test ./...
```

Coverage focuses on deterministic code paths: slugifier (against the real example URL), cache (TTL + LRU eviction), rate limiter, budget counter, input validation, demo client, circuit breaker (trip + half-open recovery), api-key extraction, and the scrape client mapping against **recorded apigee fixtures**.

A live, network-hitting smoke test exercises the real Hostelworld backend end to end (search → details → rooms) and the api-key bootstrap. It is skipped by default; run it with:

```bash
HW_LIVE=1 go test ./internal/hostelworld/ -run TestLiveScrape -v
```

## Why "unofficial"

This project is not affiliated with Hostelworld. It reads the same public PWA backend the website uses and drives qualified bookings into Hostelworld's own checkout flow rather than around it. Because it scrapes an unofficial endpoint, it deliberately stays polite (low global QPS, an in-flight cap, a daily budget) and degrades cleanly via a circuit breaker when the upstream changes. Anyone deploying it should review Hostelworld's terms of use first; scraping posture is a judgement call, not an endorsement.

## License

To be added when the project becomes runnable against the live API.
