# hostelworld-mcp

An unofficial, public **Model Context Protocol (MCP)** server for the [Hostelworld](https://www.hostelworld.com) Partner API, written in Go. Lets AI assistants like ChatGPT and Claude search hostels and produce a booking deep-link inside a natural conversation.

> **Status: design-complete reference implementation.** A Hostelworld Partner API key is required to run against the real API; we are waiting on key approval. Until then the server runs in **demo mode** with embedded fixture responses, so the code is end-to-end exercisable without credentials.
>
> The architectural rationale behind every layer (transport, tools, rate limiting, budget cap, error taxonomy) is in [`DESIGN.md`](DESIGN.md). Each design decision is documented with the alternatives considered and why they were rejected.

## What it does

Three tools, registered with the MCP client:

| Tool | Purpose |
| --- | --- |
| `search_hostels` | Find properties by city, dates, guest count. Supports `exclude_ids` so "show me different ones" works without re-showing duplicates. |
| `get_hostel_details` | Rooms, prices, amenities for a specific property. |
| `get_booking_url` | A pre-filled `hostelworld.com` deep link — payment happens on Hostelworld's site; this server never touches it. |

## Quickstart (demo mode, no key needed)

```bash
cp .env.example .env       # demo mode is the default
go run ./cmd/hostelworld-mcp
```

The server listens on `127.0.0.1:8080`. The MCP endpoint is `POST /mcp` (Streamable HTTP transport). `GET /healthz` is unauthenticated and used for liveness checks.

To exercise it without writing an MCP client, point [`mcp-inspector`](https://github.com/modelcontextprotocol/inspector) at `http://127.0.0.1:8080/mcp` and call the tools interactively.

## Running against the real API (once the key arrives)

```bash
HOSTELWORLD_DEMO=false \
HOSTELWORLD_API_KEY=your_key_here \
go run ./cmd/hostelworld-mcp
```

Field names in `internal/hostelworld/client.go` are marked `// TODO(api):` where they are best-effort pending live-API verification — expect a small reconciliation pass once we can hit production.

## Configuration

All configuration is via environment variables (see [`.env.example`](.env.example)):

| Variable | Default | Purpose |
| --- | --- | --- |
| `HOSTELWORLD_DEMO` | `true` | Use embedded fixtures instead of the real API. |
| `HOSTELWORLD_API_KEY` | — | Required when not in demo mode. |
| `LISTEN_ADDR` | `127.0.0.1:8080` | HTTP listen address. |
| `DAILY_BUDGET` | `10000` | Upstream calls per UTC day before the hard cap. |
| `SOFT_CAP_PCT` / `HARD_CAP_PCT` | `70` / `95` | Thresholds for cache-only / refuse-all states. |
| `RATE_BUCKET` / `RATE_REFILL_PER_SEC` | `20` / `0.2` | Per-IP token bucket sizing. |
| `GLOBAL_QPS` / `GLOBAL_BURST` | `5.0` / `10` | Hard ceiling on upstream calls per second. |
| `REAL_IP_HEADER` | — | Set to `CF-Connecting-IP` when behind Cloudflare. |

## Design notes worth surfacing

These details are easy to miss if you only read the code:

- **No auth.** The MCP endpoint is open by design — this is an unofficial public MCP. Protection comes from per-IP rate limits and a daily upstream-call budget cap, not credentials. See [`DESIGN.md` §9](DESIGN.md#9-authentication).
- **No conversation state.** "Show me more without duplicates" is solved by passing `exclude_ids` from the model on each call, not by a server-side session store. See [§6](DESIGN.md#6-conversation-state-the-show-me-more-problem).
- **Availability is not cached.** Static property info and city-name lookups are; prices are not, to avoid showing a price that's stale when the user reaches Hostelworld's checkout. See [§12](DESIGN.md#12-caching).
- **Payment stays on hostelworld.com.** `get_booking_url` returns a deep link to Hostelworld's existing property page. We never touch cards. See [§8](DESIGN.md#8-booking-handoff).

## Project layout

```
.
├── cmd/hostelworld-mcp/         # main.go — wiring, lifecycle
├── internal/
│   ├── budget/                  # daily upstream-call cap with on-disk persistence
│   ├── cache/                   # TTL + LRU wrapper
│   ├── config/                  # env-loaded config with redacted Stringer
│   ├── errors/                  # taxonomy (codes + safe/internal faces)
│   ├── hostelworld/             # Partner API client (HTTP + demo)
│   │   └── fixtures/            # embedded JSON for demo mode
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

Coverage focuses on deterministic, key-independent code paths: slugifier (against the real example URL), cache (TTL + LRU eviction), rate limiter (burst + per-key isolation), budget counter (state transitions + on-disk persistence), input validation, demo client. No contract tests against the real API — those will be added once a key is available.

## Why "unofficial"

This project is not affiliated with Hostelworld. It exists to bridge an emerging open protocol (MCP) with their public-facing inventory, in a way that drives qualified bookings into their own checkout flow rather than around it. We've reached out to their partner team for the API key and explicit permission; the project will only see real traffic once that approval is in hand.

## License

To be added when the project becomes runnable against the live API.
