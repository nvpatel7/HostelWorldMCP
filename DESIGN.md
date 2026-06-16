# Hostelworld MCP Server — Design Document

**Status:** Implemented and running against the live site.
**Audience:** Implementer (you) and reviewers.
**Goal of this doc:** Lock the architecture and the contracts. Every section names the chosen approach, the alternatives considered, and why each alternative was rejected.

> **v2 pivot (data source):** The Hostelworld Partner API key never arrived, so the
> server no longer targets a contracted Partner API. Instead it **scrapes Hostelworld's
> public PWA JSON backend** (`prod.apigee.hostelworld.com`) on demand — the same endpoints
> `www.hostelworld.com/pwa` calls. Because that backend is unofficial and can change without
> notice, all upstream access is wrapped in a **circuit breaker** plus conservative rate
> limits ([§7](#7-hostelworld-data-integration-scraping), [§11.7](#117-circuit-breaker)).
> Sections below are updated for this; the tool API ([§5](#5-tool-api-design)), transport
> ([§3](#3-mcp-transport)), state model ([§6](#6-conversation-state-the-show-me-more-problem)),
> and booking handoff ([§8](#8-booking-handoff)) are unchanged.

---

## 1. Goals & Non-Goals

### 1.1 Goals
- Expose Hostelworld's public inventory to LLM clients (OpenAI Responses API, and any other MCP client) through a Model Context Protocol server written in Go, by **scraping the public PWA JSON backend** (no Partner API key — see the v2 pivot note above).
- Support a multi-turn conversational flow: search → "show me different ones" → drill into a property → produce a booking link.
- Run as an **unofficial, public MCP**: any internet user can point their MCP client at our URL. We are not affiliated with Hostelworld; we read the same public backend the website uses.
- Stay resilient when the unofficial upstream breaks: a circuit breaker degrades to a clean error instead of hammering a changed/broken endpoint.
- Never expose the scraped api-key to the LLM client or to end users (it is the PWA's public key, but we still keep it out of tool responses and logs).
- Enforce abuse limits (per-IP and global) to protect *our* upstream quota and our wallet.
- Stay within a hard daily/monthly upstream-call budget; degrade gracefully (refuse new searches with a clear message) before exceeding it.
- Deploy as a single binary, runnable locally (stdio) or hosted (HTTP).

### 1.2 Non-Goals
- We do **not** complete payment in-process. The user is handed a deep-link to hostelworld.com to complete the booking. (Hostelworld's partner API has no documented checkout endpoint.)
- We do **not** ship a UI. The chat client (ChatGPT, Claude Desktop, etc.) is the UI.
- We do **not** persist long-term user data (no account system, no booking history). All state is conversation-scoped.
- We do **not** attempt to defeat true network-layer DDoS. Rate limiting here protects our upstream API quota; volumetric attacks need a CDN/WAF layer in front (Cloudflare, etc.) — called out under [§15 Deployment](#15-deployment).
- We do **not** authenticate end users in v1. The MCP endpoint is open to the internet, gated only by per-IP rate limits and a global budget cap. A signup/token tier is a v2 option ([§9](#9-authentication)).
- We do **not** earn affiliate revenue in v1 unless Hostelworld provides a referral parameter (open question — [§18](#18-open-questions)).

### 1.3 Success Criteria
1. A user in ChatGPT can say "find me a hostel in Lisbon for 2 people, May 10–13" and get back a list of properties.
2. They can say "show me different ones" and the same properties don't appear.
3. They can say "what rooms does the second one have" and get rooms + prices.
4. They can say "book the 6-bed dorm" and receive a working hostelworld.com URL that pre-fills the booking form.
5. The Hostelworld API key never appears in any tool response, log line, or error message.
6. A misbehaving client cannot consume more than its allotted share of the upstream quota.

---

## 2. High-Level Architecture

```
┌─────────────────┐    MCP over HTTP/SSE   ┌──────────────────────────────┐
│  OpenAI         │ ─────────────────────▶ │   Hostelworld MCP Server     │
│  Responses API  │                        │   (Go, single binary)        │
│  (or any MCP    │ ◀───────────────────── │                              │
│   client)       │       tool results     │  ┌────────────────────────┐  │
└─────────────────┘                        │  │ MCP transport layer    │  │
                                           │  ├────────────────────────┤  │
                                           │  │ Auth middleware        │  │
                                           │  │ (bearer token)         │  │
                                           │  ├────────────────────────┤  │
                                           │  │ Per-client rate limit  │  │
                                           │  ├────────────────────────┤  │
                                           │  │ Tool handlers          │  │
                                           │  │  - search_hostels      │  │
                                           │  │  - get_hostel_details  │  │
                                           │  │  - get_booking_url     │  │
                                           │  ├────────────────────────┤  │
                                           │  │ Hostelworld API client │  │
                                           │  │ (global rate limiter,  │  │
                                           │  │  response cache)       │  │
                                           │  └───────────┬────────────┘  │
                                           └──────────────┼───────────────┘
                                                          │ HTTPS
                                                          ▼
                                           ┌──────────────────────────────┐
                                           │ Hostelworld public PWA backend│
                                           │ (prod.apigee.hostelworld.com) │
                                           │  guarded by a circuit breaker │
                                           └──────────────────────────────┘
```

**Process model:** one Go binary, stateless except for in-memory rate limiter buckets and an LRU response cache. No database. No persistent session store (see [§6](#6-conversation-state-the-show-me-more-problem)).

---

## 3. MCP Transport

### 3.1 Decision: **Streamable HTTP** (with SSE for server→client streaming)

This is the transport defined in the MCP 2025-03-26 spec revision and is what OpenAI's Responses API connects to for "remote MCP servers." A single HTTP endpoint accepts JSON-RPC POSTs from the client; the server can optionally upgrade a response to SSE for streaming.

### 3.2 Alternatives Considered

| Option | Rejected because |
|---|---|
| **stdio** | Only works when the client launches the server as a subprocess on the same machine (Claude Desktop, IDE extensions). OpenAI's hosted Responses API cannot launch local processes — it can only connect to HTTP MCP endpoints. We need HTTP. |
| **Plain HTTP/SSE (older spec)** | Deprecated in favor of Streamable HTTP. Two endpoints (`/messages` POST + `/sse` GET), more moving parts, and OpenAI's client already standardized on the newer transport. |
| **WebSocket** | Not part of the MCP spec. Some servers add it, but no client requires it and OpenAI doesn't speak it. Choosing it would lock us to bespoke clients. |

### 3.3 Implications
- Server must handle JSON-RPC 2.0 framing (request/response/notification).
- Long-running tool calls can stream progress via SSE; for our use case (quick API lookups, sub-second), we won't actually need streaming, but the transport supports it for free.
- We get standard HTTP middleware (auth, rate limit, observability) "for free" — a major reason to prefer this over stdio even for local dev. Local dev runs the binary as `./hostelworld-mcp serve --addr=127.0.0.1:8080`.

---

## 4. MCP Library

### 4.1 Decision: **`github.com/mark3labs/mcp-go`**

Most actively maintained Go MCP library, supports stdio and Streamable HTTP, has a clean tool-registration API, and tracks the spec closely. Supports tool input schemas declared in Go (struct tags or builder), which we want for both validation and tool-discovery responses.

### 4.2 Alternatives Considered

| Option | Rejected because |
|---|---|
| **`github.com/metoro-io/mcp-golang`** | Smaller community, fewer examples, transport support has lagged the spec. Viable fallback if mark3labs has a blocker. |
| **Roll our own JSON-RPC + MCP layer** | MCP isn't trivially small (initialization handshake, capability negotiation, tool/prompt/resource lifecycle, SSE framing). Three weeks of yak-shaving for zero product value. Only justifiable if both libraries fail us. |
| **Anthropic's official Go SDK** | At time of writing there is no official Anthropic-published Go MCP SDK. The TypeScript and Python SDKs are official; Go is community-driven. If Anthropic releases one later, migration is straightforward (the wire format is fixed). |

### 4.3 Risk
mcp-go is pre-1.0; breaking changes are possible. Mitigation: pin the version in `go.mod`, wrap library types behind a thin internal `mcpserver` package so a swap is contained.

---

## 5. Tool API Design

Three tools. Keep the surface small — each tool added is another thing the model has to learn to choose between, and another attack surface.

### 5.1 `search_hostels`

```json
{
  "name": "search_hostels",
  "description": "Search Hostelworld for properties in a city for given dates and guest count. Returns up to 10 properties per call. To get more results, call again with the previously returned IDs in exclude_ids.",
  "input_schema": {
    "type": "object",
    "required": ["city", "checkin", "checkout", "guests"],
    "properties": {
      "city":     { "type": "string",  "description": "City name, e.g. 'Lisbon'. Resolved to a Hostelworld location ID server-side." },
      "checkin":  { "type": "string",  "format": "date", "description": "YYYY-MM-DD, must be today or later." },
      "checkout": { "type": "string",  "format": "date", "description": "YYYY-MM-DD, must be after checkin, max 30 nights." },
      "guests":   { "type": "integer", "minimum": 1, "maximum": 16 },
      "currency": {
        "type": "string",
        "description": "ISO-4217 currency code, e.g. 'USD', 'EUR', 'GBP'. Defaults to 'USD' if omitted. Pass-through to upstream so prices come back in the user's chosen currency.",
        "default": "USD",
        "pattern": "^[A-Z]{3}$"
      },
      "exclude_ids": {
        "type": "array",
        "items": { "type": "string" },
        "description": "Property IDs already shown to the user in this conversation. Pass to avoid duplicates on follow-up calls."
      },
      "sort": {
        "type": "string",
        "enum": ["recommended", "price_low", "rating"],
        "default": "recommended"
      }
    }
  }
}
```

**Output shape (structured content):**
```json
{
  "results": [
    {
      "id": "12345",
      "name": "Yes! Lisbon Hostel",
      "rating": 9.2,
      "rating_label": "Superb",
      "price_from": { "amount": 24.50, "currency": "EUR" },
      "thumbnail": "https://…",
      "neighborhood": "Baixa",
      "tags": ["wifi", "breakfast", "24h_reception"]
    }
    // …up to 10
  ],
  "total_available": 84,
  "shown_so_far": 10
}
```

### 5.2 `get_hostel_details`

```json
{
  "name": "get_hostel_details",
  "description": "Get available rooms, prices, and amenities for a specific Hostelworld property on given dates.",
  "input_schema": {
    "type": "object",
    "required": ["property_id", "checkin", "checkout", "guests"],
    "properties": {
      "property_id": { "type": "string" },
      "checkin":     { "type": "string", "format": "date" },
      "checkout":    { "type": "string", "format": "date" },
      "guests":      { "type": "integer", "minimum": 1, "maximum": 16 },
      "currency":    { "type": "string", "default": "USD", "pattern": "^[A-Z]{3}$" }
    }
  }
}
```

### 5.3 `get_booking_url`

```json
{
  "name": "get_booking_url",
  "description": "Construct a hostelworld.com booking URL with prefilled property, room, dates, and guests. The user completes payment on hostelworld.com.",
  "input_schema": {
    "type": "object",
    "required": ["property_id", "room_type_id", "checkin", "checkout", "guests"],
    "properties": {
      "property_id":  { "type": "string" },
      "room_type_id": { "type": "string" },
      "checkin":      { "type": "string", "format": "date" },
      "checkout":     { "type": "string", "format": "date" },
      "guests":       { "type": "integer", "minimum": 1, "maximum": 16 }
    }
  }
}
```

### 5.4 Why three tools, not one or seven?

| Granularity | Rejected because |
|---|---|
| **One mega-tool** (`hostelworld_action(action=…)`) | Models reason much better when each tool has a focused description and schema. A union-typed action parameter is a known antipattern in tool-use literature — the model often picks the wrong sub-action. |
| **Seven tools** (separate `search_by_city`, `search_by_landmark`, `filter_by_rating`, `get_rooms`, `get_amenities`, `get_reviews`, `book`) | We don't have product use cases for most of them yet. Each tool the model sees in its system prompt costs tokens and adds a chance of misrouting. Add tools only when a clear flow demands them. |

### 5.5 Schema design notes
- `exclude_ids` is on the **client side** of the contract — the model passes IDs it has already seen back in. See [§6](#6-conversation-state-the-show-me-more-problem) for why.
- All dates are `YYYY-MM-DD` ISO strings, validated server-side. Rejecting bad dates with a clear error message helps the model self-correct on the next turn.
- Output uses MCP's `structuredContent` field in addition to a human-readable text block. The structured field is what the model actually parses; the text is a fallback for clients that don't support structured output.

---

## 6. Conversation State: The "Show Me More" Problem

### 6.1 Decision: **Model-tracked exclusions** (option A)

The tool returns property IDs in its structured output. On follow-up calls, the model passes those IDs back in `exclude_ids`. The server is fully stateless across requests.

### 6.2 Alternatives Considered

| Option | Tradeoffs / why rejected (or kept as fallback) |
|---|---|
| **A. Model-tracked exclusions** *(chosen)* | ✅ Stateless server. No session store, no TTLs, no eviction, no cross-instance synchronization. ✅ Naturally scoped to the conversation — when the user starts a new chat, the slate is clean automatically. ⚠️ Relies on the model to remember and pass back IDs. In practice, frontier models do this reliably for <50 IDs. ⚠️ Token cost: 50 IDs × ~6 chars = ~300 tokens per call; negligible. |
| **B. Server-side session store** (Redis-keyed by session_id) | ❌ Requires the MCP client to send a stable session ID. The MCP spec does include a `Mcp-Session-Id` header, but its semantics are about transport sessions, not conversation sessions. We'd be overloading it. ❌ Adds a dependency (Redis or in-memory map with TTL eviction). ❌ TTL choice is awkward: too short and the user loses state mid-conversation; too long and we leak memory. ❌ Multi-instance deployments need shared state. |
| **C. Hybrid (server-side hint, model can override)** | ❌ More complex than either pure option. The model is good enough at A that we don't need this. |

### 6.3 Failure mode of the chosen approach
If the model hallucinates IDs or drops some from context, the user sees a duplicate. This is annoying but not destructive. We log when `exclude_ids` overlaps with returned IDs (means upstream returned a duplicate or model passed a stale ID) for debugging.

### 6.4 Switching to B later
If we observe duplication in production logs >5% of follow-up calls, we'll add a server-side session store keyed by `Mcp-Session-Id` as a belt-and-suspenders deduplication layer, *while keeping* `exclude_ids` as the primary mechanism (defense in depth).

---

## 7. Hostelworld Data Integration (Scraping)

No Partner API key was ever granted, so we read the **same JSON backend the public PWA uses**.
`www.hostelworld.com/pwa` is a Nuxt single-page app; its data comes from
`https://prod.apigee.hostelworld.com`. We call those endpoints directly with the public
`api-key` the PWA ships to every browser. Implemented in `internal/hostelworld/scrape.go`
(transport) + `apigee.go` (response shapes → our tool-facing types) + `apikey.go` (key
bootstrap). All shapes were captured from live responses and frozen into
`internal/hostelworld/fixtures/apigee_*.json` for tests.

### 7.1 Endpoints we use (verified live)

| Our tool | Upstream call(s) |
|---|---|
| `search_hostels` | Resolve city: `GET /autocomplete-service/v1/autocomplete/web?text={city}` → `[{id,name,type}]`; take the first `type=="city"`. Then `GET /legacy-hwapi-service/2.2/cities/{id}/properties/?number-of-guests=N&date-start=YYYY-MM-DD&num-nights=N&page=1&application=web&currency=…` → `{properties:[…], pagination:{totalNumberOfItems}}`. |
| `get_hostel_details` | `GET /legacy-hwapi-service/2.2/properties/{id}/?application=web` (static info) + `GET /legacy-hwapi-service/2.2/properties/{id}/availability/?date-start=…&num-nights=N&number-of-guests=N&application=web&currency=…` → `{rooms:{dorms:[…],privates:[…]}}`. |
| `get_booking_url` | No upstream call. We construct a URL deterministically — see [§8](#8-booking-handoff). |

Notes on the shapes we map: the city id namespace (`id=15` for Amsterdam) is the *autocomplete*
id and is also what `cities/{id}/properties/` wants — it is **not** the `property-group-id` the
generic `/properties/` endpoint expects. Prices arrive as `{"value":"26.04","currency":"USD"}`
strings; ratings as `overallRating.overall` on a **0–100** scale (we divide by 10). Dates are
expressed to the upstream as `date-start` + `num-nights` (derived from checkin/checkout).

### 7.2 The api-key (bootstrap, not a secret we own)

The api-key is the public key the PWA embeds in its Nuxt runtime config as `APIGEE_KEY:"…"`.
We bootstrap it by fetching a PWA page and extracting it with a regex (`apikey.go`), caching it
(6h TTL) and **force-refreshing once on a 401** so we automatically track Hostelworld's
rotations. A compiled-in last-known-good key and a `HOSTELWORLD_APIGEE_KEY` override are the
fallbacks. We still keep it out of tool responses and logs ([§10](#10-secret-management)).

### 7.3 City resolution + caching

A user types "Amsterdam" but the search endpoint wants a numeric city id — one autocomplete hop
per first-time search. We cache the city-name → id mapping aggressively (24h TTL, LRU) so the
common case collapses to one upstream call. A name with no autocomplete match returns the
plain-text apology the service emits; we treat that (and any non-array body) as `not_found`,
not a service error.

### 7.4 Client design
- Single `ScrapeClient` holding the `http.Client`, apigee base URL, `keyProvider`, global rate
  limiter, an **in-flight concurrency semaphore**, the **circuit breaker**, and the two LRU
  caches (city id, property static info).
- `Search` / `Details` take a `context.Context` and return the same typed structs the tools
  already used — the `Client` interface is unchanged, so handlers and the demo client are untouched.
- All calls go through one `getRaw()` helper, wrapped by the circuit breaker, that handles:
  rate-limit acquire, concurrency cap, api-key injection, retries on 429/5xx (exponential
  backoff, max 3), a single key-refresh on 401, status→error mapping, and JSON unmarshal.
  Business outcomes (404 → `not_found`) are returned *without* tripping the breaker; transport
  and 5xx failures count toward tripping it.

### 7.5 Alternatives Considered

| Option | Rejected because |
|---|---|
| **Parse the SSR HTML / `window.__NUXT__` payload** | The page does embed the search results, but as a minified Nuxt *function* blob (deduplicated variables, not JSON). Parsing it reliably needs a JS engine and breaks on every layout tweak. The JSON backend is stable, typed, and paginated. |
| **Headless browser (Playwright/chromedp)** | Heavy, slow, and a large attack/ops surface for what is a set of plain GET requests. Only justified if the backend moved behind bot-JS challenges (it hasn't for these endpoints). |
| **Hardcode the api-key** | Rotates; would silently break. We scrape it and refresh on 401 instead, keeping a hardcoded value only as a last-resort fallback. |
| **No caching** | City resolution would double upstream traffic for repeat searches. Free win to cache (ids don't move). Availability is still never cached ([§12](#12-caching)). |

---

## 8. Booking Handoff

### 8.1 The problem
There is no checkout endpoint we can drive (and we wouldn't want to handle payment anyway). The user must complete payment on hostelworld.com.

### 8.2 Decision: **Construct deep links by URL pattern**

Hostelworld's public site uses a predictable URL for the property detail page (which has the "Book" CTA leading to their own checkout). Confirmed pattern from a real example:

```
https://www.hostelworld.com/pwa/hosteldetails.php
  /{property-slug}/{city-slug}/{property-id}
  ?from={YYYY-MM-DD}
  &to={YYYY-MM-DD}
  &guests={N}
```

Real example provided by partner team:
```
https://www.hostelworld.com/pwa/hosteldetails.php/Revolution-Khao-San-by-The-Bliss/Bangkok/326223?from=2026-05-21&to=2026-05-28&guests=1
```

The user clicks "Book" on that page; Hostelworld then funnels them to `https://www.hostelworld.com/pwa/checkout` where payment happens. We never see the checkout flow — it's entirely on hostelworld.com's session.

**Inputs we have:** `property_id` (from search results), `checkin`, `checkout`, `guests`. **Inputs we need to derive:** `property-slug` and `city-slug` — these come from the property detail API response (likely fields like `name` and `city.name`, slugified). We slugify them server-side: lowercase → spaces and special chars to `-` → collapse repeats → trim. Implement once, contract-test against the real URL.

**Note on `room_type_id`:** the booking URL pattern doesn't take a room ID — Hostelworld's page lets the user pick the room themselves. We'll keep `room_type_id` in the `get_booking_url` tool schema so the model can communicate the chosen room to the user in chat, but the URL itself doesn't encode it. Reconsider if the user reports friction here.

### 8.3 Alternatives Considered

| Option | Rejected because |
|---|---|
| **Email / contact partner support for a documented booking deep-link** *(do this in parallel)* | Not rejected — we'll send the email, but we can't block the build on it. Deep linking is the practical fallback. |
| **Render a checkout flow ourselves and process payment** | We are not a PCI-compliant entity. Multiple orders of magnitude more work, plus liability we don't want. |
| **Open-redirect endpoint on our server that 302s to hostelworld.com** | Slightly cleaner UX (we own the URL the user clicks), but adds a stateful component (we need to log the redirect for analytics) and another DDoS surface. The model can return the hostelworld.com URL directly; the client renders it as a link. |

### 8.4 Risks
- Hostelworld may change their public URL pattern. **Mitigation:** isolate URL templating in one function (`buildBookingURL`) covered by a contract test that fetches a known property and asserts the page returns 200. Run the contract test in CI weekly to catch drift.
- Slugification mismatch — if our slug algorithm produces a different slug than Hostelworld's canonical one, the URL might 404 or 301-redirect. **Mitigation:** the property details API may return a canonical slug field directly; if so, prefer it over slugifying ourselves. Confirm during milestone 4.
- We don't earn affiliate commission unless we attach a partner ID. **TBD:** confirm whether the partner API issues a referral parameter we should append (e.g. `?source=partner-{id}`). Open question for [§18](#18-open-questions).

---

## 9. Authentication

Two layers, distinct concerns. The unofficial-public-MCP framing changes layer 2 substantially.

### 9.1 MCP server ⇄ Hostelworld backend
The apigee backend requires an `api-key` header. This is the **public key the PWA ships to
browsers**, not a partner secret, so there is no credential for us to hold — we **bootstrap it
from the PWA page at runtime** and refresh on rotation ([§7.2](#72-the-api-key-bootstrap-not-a-secret-we-own)).
An operator may pin it via `HOSTELWORLD_APIGEE_KEY`. We still keep it out of every tool response
and log line ([§10](#10-secret-management)).

### 9.2 MCP client ⇄ MCP server

#### Decision: **Open access in v1, gated by per-IP rate limits + global budget cap. Optional signup-for-higher-tier in v2.**

The endpoint is reachable by anyone on the internet without credentials. Protection comes from rate limits, not auth:

- Per-IP token bucket ([§11](#11-rate-limiting--abuse-protection)) keeps any single caller from monopolizing the upstream quota.
- Global budget cap ([§11.4](#114-layer-3-global-budget-cap-new--critical-for-public-model)) refuses *all* searches when daily upstream calls approach our partner-tier ceiling.
- Cloudflare in front gives us bot-detection, country blocking, and challenge-based mitigation if abuse is sustained ([§15](#15-deployment)).

This is the only realistic v1 model given the constraints:
- We can't issue bearer tokens to "the general public" — there's no signup flow yet.
- We can't require OAuth — there's no identity provider, and most general-purpose MCP clients won't go through an OAuth dance for a casual lookup.
- BYOK (user supplies their own Hostelworld key) is a non-starter — Hostelworld doesn't issue partner keys to consumers.

#### Alternatives Considered

| Option | Outcome |
|---|---|
| **Bearer token, manually issued** *(my v1 in the previous draft)* | ❌ Doesn't fit "general public." Implied a relationship that doesn't exist. |
| **OAuth 2.1 with dynamic client registration** *(MCP spec recommends)* | ✅ The right answer if/when we have a user system. ❌ Premature for v1: weeks of build (registration, token, refresh, scopes) with no users to authenticate. Reconsider when v2 introduces signups. |
| **No auth, no rate limit** | ❌ One scraper drains our quota in an hour. Not viable. |
| **Open access with rate limit** *(chosen)* | ✅ Ships in days. Protects quota. Acceptable tradeoff for an unofficial MCP. |
| **mTLS** | ❌ MCP clients can't easily attach certs. Eliminates ChatGPT, Claude Desktop. |
| **IP allowlist** | ❌ Public service can't allowlist. |
| **CAPTCHA / proof-of-work on first request** | ❌ MCP clients aren't browsers; no CAPTCHA UI surface. PoW is novel and adds latency. |

#### v2 — optional signup tier (sketch, not building yet)

If abuse becomes a problem, or we want to offer a higher rate-limit tier:

1. A small static site (`mcp.hostelworld-unofficial.example/signup`) issues bearer tokens after email verification.
2. Requests with `Authorization: Bearer …` get a higher per-IP-or-token limit (e.g. 5×); anonymous traffic stays at the v1 limit.
3. Tokens are revocable when abuse is identified.

The middleware is structured so this slots in: an optional auth lookup populates a `principal` (anonymous or token-id), and the rate limiter keys on `principal_or_ip`. v1 ships without the signup site; v2 adds it.

#### Sanity check on the threat model
The realistic adversary is not a sophisticated attacker — it's:
- A curious user who scripts the MCP and accidentally floods us. → Per-IP limit catches it.
- A few users pointing aggressive agents at us. → Per-IP limit + global cap.
- A deliberate attacker trying to drain our quota. → Per-IP limit + Cloudflare bot rules. They can rotate IPs, but at significant cost; the global cap means even successful abuse stops at our budget ceiling.

We are not defending against nation-state actors or competent botnets. If that materializes, the right answer is to take the service down and add real auth, not to over-engineer v1.

---

## 10. Secret Management

### 10.1 Decision: **Environment variables for v1; secret manager for hosted v1.5**

- Local dev: `.env` file loaded by `godotenv`, `.env` in `.gitignore`, committed `.env.example` with placeholder values.
- Hosted: env vars injected by the platform (Fly.io secrets, Railway variables, Cloud Run secret refs, etc. — depends on [§11](#11-deployment)).
- Code never accepts a secret as a CLI flag (would leak into `ps`, shell history).

### 10.2 What's a secret here
- `HOSTELWORLD_APIGEE_KEY` — *technically public* (the PWA hands it to every browser), but we
  treat it as sensitive config: masked in `Config.Redacted()`, kept out of tool responses and
  logs. We don't own it, so there is nothing to rotate on our side beyond re-scraping.
- Server bind address, port, log level — config, not secrets. Fine in flags or non-sensitive env.

There is no longer any partner secret or bearer-token secret to manage (the Partner API path
was removed; the MCP endpoint is open — [§9.2](#92-mcp-client--mcp-server)).

### 10.3 Defense-in-depth: secret hygiene

| Threat | Mitigation |
|---|---|
| Secret accidentally logged | Wrap secrets in a `Secret` type whose `String()` returns `"[REDACTED]"`. Only `.Reveal()` returns the actual value, and it's only called at the HTTP-call boundary. Catches the entire class of `log.Printf("config: %+v", cfg)` mistakes. |
| Secret echoed in tool error response | Error mapping layer never embeds raw upstream response bodies in tool errors. We map to a stable set of error codes ([§13](#13-error-handling)). |
| Secret in panic stack trace | Secret types implement `GoString()` to redact in `%#v` formatting too. |
| Secret in core dump | Out of scope for v1; relevant only at higher security tiers. |

### 10.4 Alternatives Considered

| Option | Rejected because |
|---|---|
| **Hardcoded constants** | Obvious no. |
| **Config file with secrets** | Risk of accidental commit. Env vars are easier to keep out of git and easier to inject in containers. |
| **Vault / cloud KMS for v1** | Real value at scale but premature for a single-binary deployment with one upstream API key. |

---

## 11. Rate Limiting & Abuse Protection

This is the *primary* defense layer in the open-access model — auth's job in a closed system is being done here.

### 11.1 Three independent layers

```
Request ──▶ [per-IP limiter] ──▶ [tool handler] ──▶ [global rate limiter] ──▶ [budget cap] ──▶ Hostelworld
            (token bucket             (token bucket           (today's call counter,
             per IP)                   on client struct)       refuses if over budget)
```

### 11.2 Layer 1: Per-IP (token bucket)

#### Decision: **Token bucket via `golang.org/x/time/rate`, keyed by client IP**

- Bucket: 20 tokens, refill 0.2/sec (= ~12 req/min sustained, allows short burst of 20). Tunable; start conservative.
- IP extracted via the `CF-Connecting-IP` header when behind Cloudflare, falling back to the leftmost untrusted entry in `X-Forwarded-For`, falling back to `RemoteAddr`. Configure trusted-proxy IPs explicitly to prevent spoofing.
- Stored in `sync.Map[string]*rate.Limiter`; entries evicted after 10min of inactivity by a janitor goroutine. (Memory ceiling: ~10k buckets × ~80B = 800KB, fine.)
- Returns HTTP 429 with `Retry-After` header when exhausted. The MCP error response carries a structured `rate_limited` code so the model can communicate the wait to the user instead of retrying immediately.

#### Alternatives Considered

| Option | Rejected because |
|---|---|
| **Key by bearer token** *(my previous draft)* | ❌ No tokens in v1; flipped to IP. Will become "key by token-or-IP" if v2 signup tier ships. |
| **Sliding window log** | More accurate (no burst leakage) but ~10× the memory/CPU. Token bucket is the right tradeoff for our scale. |
| **Fixed window** | Cheap but allows 2× burst at window boundary. Token bucket avoids it. |
| **Redis-backed limiter** | Required for multi-instance horizontal scale. Overkill for v1 single-instance. Migrate when needed; the limiter interface is small. |
| **No per-IP limit, rely only on global cap** | ❌ One scraper drains the global budget for everyone. Per-IP is the cheap, fair first line of defense. |

#### Honest weakness of IP-keying
- IPv6 makes per-IP almost free for an attacker to rotate (massive address space). Mitigation: rate-limit on /64 prefix for IPv6, not full address.
- Shared NATs (offices, mobile carriers) punish innocent users who share an IP with a heavy one. Acceptable for v1; tier-up via signup is the v2 fix.
- Tor / VPN exits get hit hardest. We're OK with that for an unofficial public MCP — anyone routing aggressive agents through Tor is the abuse case we want to penalize.

### 11.3 Layer 2: Global upstream rate limiter

A single `rate.Limiter` on the Hostelworld client struct. Sized to ~80% of our partner-tier QPS. Protects the *instantaneous* upstream rate when many IPs each within their per-IP limit collectively burst.

When exhausted, the request waits (up to a 5s deadline from the request context); if it doesn't acquire, returns a `service_busy` error.

### 11.4 Layer 3: Global budget cap (NEW — critical for public model)

#### Decision: **Daily upstream-call counter; hard refuse when over budget**

A single in-process counter (`atomic.Int64`) tracks total upstream calls made *today* (UTC midnight reset). Two thresholds:

- **Soft cap (e.g. 70% of daily quota):** start serving cached results only; refuse new searches that would miss the cache. Log warning. Optionally email/page the operator.
- **Hard cap (e.g. 95%):** refuse all upstream calls, return `quota_exhausted` error to the model: *"Sorry, daily search quota reached — try again after midnight UTC."* The model can communicate this to the user clearly.

Persisted to disk on shutdown (so a process restart doesn't reset the counter mid-day) and reloaded on startup; if the file is missing/corrupt, start at zero and log a warning.

#### Why this matters
The global *rate* limiter ([§11.3](#113-layer-2-global-upstream-rate-limiter)) keeps QPS in check, but doesn't bound *total* daily spend. Without a budget cap, a slow steady abuser within rate limits could still drain a 50K-call/day quota in 6 hours and leave nothing for legitimate users for the rest of the day. The budget cap is the safety belt that makes the public model viable.

#### Alternatives Considered

| Option | Rejected because |
|---|---|
| **No budget cap, trust the rate limiter** | ❌ Slow drains succeed. Public quota goes to zero. |
| **Per-IP daily cap instead of global** | ✅ Useful, but doesn't replace global — 100 IPs × 1000 calls/day each = 100K. Add later as a refinement. |
| **Dollar budget instead of call count** | Overkill; partner tier is call-quota, not pay-per-call. If pricing changes, revisit. |

### 11.5 Honest scope statement
This is **abuse protection and quota stewardship**, not DDoS mitigation. A volumetric attack (millions of requests/sec) needs a CDN or WAF in front (Cloudflare, AWS Shield) — see [§15](#15-deployment). Cloudflare's free tier is non-optional for the public model; we wire it in from milestone 11.

### 11.6 Other abuse vectors

| Vector | Mitigation |
|---|---|
| Ridiculous date ranges (5-year stay) → expensive upstream call | Schema validation: max 30 nights. |
| Huge `exclude_ids` arrays | Cap at 200 entries server-side; return a clear error above. |
| Malformed JSON → Go panics in handlers | mcp-go library handles unmarshaling; our handlers receive typed structs. Add a `recover()` middleware as backstop, log + return generic 500. |
| Slowloris (drip-feeding bytes) | `http.Server.ReadHeaderTimeout = 5s`, `ReadTimeout = 30s`, `WriteTimeout = 30s`, `IdleTimeout = 60s`. |
| Request body bombs | `http.MaxBytesReader` capped at 1 MiB on the request handler. |

### 11.7 Circuit breaker (NEW — load-bearing for the scraping model)

#### Decision: **One circuit breaker around all apigee operations** (`internal/breaker`, wrapping `sony/gobreaker/v2`)

The rate limiter and budget cap protect Hostelworld and our quota *from us*. The circuit breaker
protects *us from a broken Hostelworld* — the failure mode the scraping pivot introduces. If the
backend changes shape, rotates the key in a way we can't recover, blocks us, or goes down, we must
not keep firing requests into it (each one pays a full timeout, piles up goroutines, and looks like
an attack from their side).

- **Trip:** after `BREAKER_MAX_FAILURES` consecutive failed operations (default 5) the breaker
  opens. A failed *operation* = one logical upstream call that exhausted its internal retries
  (429/5xx with backoff, max 3) or failed to bootstrap the key.
- **Open:** for `BREAKER_COOLDOWN_SECS` (default 30) every tool call **fails fast** with a
  `service_busy` error carrying `retry_after_seconds`, and **no request touches the upstream**.
  This is the "fail fast, honest error" behaviour we chose over serving stale cache or demo data —
  showing wrong prices on a booking tool is worse than a clear "temporarily unavailable".
- **Half-open:** after the cooldown a single probe is allowed through; success closes the breaker,
  failure re-opens it.
- **Doesn't trip on business outcomes:** a `not_found` (city/property 404) is a normal answer, not
  an outage, so it is returned without counting against the breaker.

State transitions are logged (`closed→open→half-open`) so an operator can see breakage in the logs;
a future refinement is a Prometheus gauge for breaker state ([§14.2](#142-metrics)).

#### Alternatives Considered

| Option | Rejected because |
|---|---|
| **No breaker, rely on per-request timeouts** | Every request still pays the full timeout while the upstream is down; goroutines and latency pile up, and we keep hammering a broken/blocking endpoint — the opposite of polite. |
| **Serve stale cache / demo data when scraping fails** | Keeps results flowing but risks showing outdated prices on a tool whose whole point is a real booking. Honesty beats a smooth-but-wrong answer here. |
| **Per-endpoint breakers** | More granular, but the endpoints share one host and one api-key, so the dominant failure modes (key, host, block) are global. One breaker is simpler and matches "if scraping breaks, we're down." Revisit if one endpoint proves independently flaky. |
| **Hand-rolled breaker** | ~70 lines, but `sony/gobreaker/v2` is zero-dependency, battle-tested, and gets half-open semantics right. Wrapped behind our thin `breaker` package so it's swappable. |

---

## 12. Caching

### 12.1 What we cache
| Data | Layer | TTL | Why |
|---|---|---|---|
| City name → location ID | LRU in-process | 24h | Cities don't move; saves 50% of upstream traffic. |
| Property static info (name, address, amenities) | LRU in-process | 1h | Changes rarely; multiple users may search same city. |
| Property availability/pricing | **Not cached** | — | Inventory and prices change minute-to-minute; serving stale data could lead to a booking failure on hostelworld.com. |

### 12.2 Library choice
`github.com/hashicorp/golang-lru/v2` for sized LRU. Well-tested, no dependencies.

### 12.3 Alternatives Considered

| Option | Rejected because |
|---|---|
| **Cache availability for 60s** | Too risky. A user clicking "book" on a stale price gets an unexpected change at checkout. Bad trust outcome. |
| **Redis cache** | Adds infra. In-process is fine until we scale horizontally. |
| **No cache at all** | Wasteful; every "more results" call re-resolves the same city ID. |

---

## 13. Error Handling

### 13.1 Principles
1. The model is a user too. Error messages need to be actionable enough that the model can self-correct or explain the failure to the human.
2. Never include upstream raw response bodies in errors returned to the client. (Defense against accidental key/PII leakage and against confusing the model with implementation details.)
3. Every error has a stable machine-readable `code` plus a human-readable `message`.

### 13.2 Error code taxonomy (returned in MCP `isError` tool result)

| Code | When | Model should… |
|---|---|---|
| `invalid_input` | Schema violation, bad dates, etc. | Re-ask the user for the missing/correct field. |
| `not_found` | City / property doesn't exist. | Tell user, suggest checking spelling. |
| `no_availability` | No properties match. | Suggest broader dates or different city. |
| `rate_limited` | Per-client or global limit hit. | Tell user to wait; carry `retry_after_seconds`. |
| `service_busy` | Upstream slow / unreachable. | Suggest retry shortly. |
| `service_error` | Upstream 5xx after retries, our bug, etc. | Apologize, no PII, log server-side with request_id. |

### 13.3 Internal vs external error data
Each error has two faces:
- **External:** code + safe message, returned to the MCP client.
- **Internal:** full upstream response, stack, request_id, logged server-side only.

Pattern: a single `Error` struct with both, plus a `.External()` method that returns only the safe view. Handlers always call `.External()` when forming the tool response.

---

## 14. Observability

### 14.1 Logging
- Structured JSON logs via `log/slog` (stdlib, Go 1.21+).
- Every request carries a `request_id` (UUID v4) generated by middleware, included in every log line for that request, and returned in the response header `X-Request-Id`.
- Log levels: `INFO` for request start/end, `WARN` for handled errors, `ERROR` for unhandled.
- Never log: full Authorization header, API keys, full response bodies.

### 14.2 Metrics
- Prometheus exposition on `/metrics` (separate port, not exposed publicly).
- Counters: `mcp_tool_calls_total{tool,outcome}`, `hostelworld_api_calls_total{endpoint,outcome}`, `rate_limit_rejections_total{layer,reason}`.
- Histograms: `mcp_tool_duration_seconds{tool}`, `hostelworld_api_duration_seconds{endpoint}`.
- Gauges: `cache_size{name}`, `rate_limiter_buckets`.

### 14.3 Tracing
Out of scope for v1. The request_id in logs is enough for now. If we go multi-service, add OpenTelemetry.

### 14.4 Alternatives Considered

| Option | Rejected because |
|---|---|
| **Plaintext logs** | Hard to query, hard to redact systematically. JSON is barely more code. |
| **Sentry / external error reporting** | Useful but premature for v1; adds a vendor dependency and a privacy review. |

---

## 15. Deployment

### 15.1 v1 target: **Single container on Fly.io** (or equivalent: Railway, Render, Cloud Run)

- Single binary built with `CGO_ENABLED=0` for a static, distroless-friendly image.
- Single instance, single region (closer to the user, not the upstream API — TLS handshake dominates).
- TLS terminated by the platform; we serve plain HTTP internally.
- Secrets via the platform's secret store.
- Outbound to `partner-api.hostelworld.com` via egress.

### 15.2 Why not local-only
Stdio MCP would work for personal use, but the user said "works with OpenAI" — OpenAI's Responses API needs a remote HTTPS endpoint. Hosted is required.

### 15.3 Why not Kubernetes / multi-region for v1
- One user, low traffic. A single Fly machine is overkill, K8s is absurd.
- No multi-instance coordination needed because state is in-process (rate limiter buckets, LRU). Going multi-instance would require Redis — see [§11.2](#112-layer-1-per-client-token-bucket).

### 15.4 In front of the server (non-optional for public model)

Cloudflare (free tier) sits between the internet and our Fly.io machine:

- TLS termination at the edge.
- Basic L3/L4 DDoS shielding.
- Bot Fight Mode + managed challenge for suspicious traffic.
- WAF rules to block obvious abuse patterns (path scans, oversized bodies, known-bad ASNs).
- Country-level blocking if abuse is geographically concentrated.
- Pass `CF-Connecting-IP` to our app so per-IP rate limit ([§11.2](#112-layer-1-per-ip-token-bucket)) keys correctly.

Without Cloudflare, the per-IP limiter sees Cloudflare's edge IP for every request and treats all traffic as one client — so this isn't optional, it's load-bearing.

### 15.5 Operational ceiling

The public model demands a few operational guardrails:

- **Daily quota meter on a dashboard** so we can see when we're approaching the budget cap before users do.
- **Alert at soft cap** (70%): email/Discord/Slack so the operator knows abuse may be in progress.
- **Kill switch:** an env var (`SERVICE_DISABLED=true`) that makes every tool return `service_disabled` immediately, without burning quota. Lets us shut off the public surface in seconds if something goes wrong.
- **Public landing page** at the server's root explaining: this is unofficial, not affiliated with Hostelworld, terms of acceptable use, contact email for abuse reports. Makes us look credible to anyone investigating abuse and gives Hostelworld someone to talk to if they have concerns.

---

## 16. Testing Strategy

### 16.1 Tiers

| Tier | Scope | Tools |
|---|---|---|
| **Unit** | Pure functions: URL building, schema validation, error mapping, cache key derivation. | stdlib `testing`. |
| **HTTP integration** | Scrape client against a fake apigee server. | `httptest.Server` returning recorded `apigee_*.json` fixtures. |
| **MCP integration** | Tool handlers via the mcp-go test harness — assert request → response shape. | mcp-go's test utilities. |
| **Live smoke / contract** | One env-gated test (`HW_LIVE=1`) that hits the real backend end-to-end (search → details → rooms, plus api-key bootstrap) and asserts shapes. Run manually / weekly in CI, not per-commit. | stdlib; `TestLiveScrape`. |
| **E2E (manual for v1)** | Connect ChatGPT to a deployed instance, run a full scripted conversation. | Human + checklist. |

### 16.2 What we're not building for v1
- Load tests: not until we have real traffic data.
- Chaos tests: premature for a single-instance app.
- Property-based tests: no obvious surface that benefits.

---

## 17. Build Sequence (Milestones)

Numbered to show dependencies. Each milestone is a stopping point where the system is consistent and testable.

| # | Milestone | Verifies |
|---|---|---|
| 1 | Project skeleton: `cmd/hostelworld-mcp/main.go`, config loading, `slog` setup, `/healthz` HTTP. | Binary builds, runs, responds to health. |
| 2 | Scrape client (`ScrapeClient`): apigee mapping + api-key bootstrap + `Search` (autocomplete → city properties). | `httptest` fixtures green; `HW_LIVE=1` smoke test returns real properties. |
| 3 | mcp-go server scaffolding, register `search_hostels` tool that calls #2 client. | Connect via `mcp-inspector`, see the tool, call it, get results. |
| 4 | Add `get_hostel_details` and `get_booking_url`. | Full happy path through all three tools; booking URL returns 200. |
| 5 | ~~Bearer-token auth~~ — dropped. The MCP endpoint is open ([§9.2](#92-mcp-client--mcp-server)); protection is rate limits + budget cap. | — |
| 6 | Per-IP rate limiter. | Test that the bucket-exhausting request is 429'd. |
| 7 | Global upstream rate limiter + in-flight cap + retry-with-backoff. | Confirm via injection of fake 429/5xx from upstream. |
| 8 | **Circuit breaker** around the scrape client. | Breaker trips after N upstream 5xx; open returns `service_busy` without calling upstream; half-open recovers. |
| 9 | Caching (city id + property static). | Cache-hit test; verify availability is *not* cached. |
| 10 | Error taxonomy + redaction wrapper. | Error tests; key-redaction tests. |
| 11 | Metrics + structured logs (incl. breaker state transitions). | `/metrics` returns Prometheus exposition. |
| 12 | Containerize, deploy to Fly.io behind Cloudflare. | Connect ChatGPT → run scripted conversation end-to-end. |
| 13 | Contract/CI: weekly live check of endpoint shapes + booking URL. | Drift in the scraped shapes or booking pattern is caught. |

Estimate: 1-2 weeks for a focused build, depending on Hostelworld API quirks.

---

## 18. Open Questions

### 18.1 Resolved

| # | Question | Answer |
|---|---|---|
| 1 | Hostelworld partner credentials? | **No key yet.** Operator will email Hostelworld to request one. *Blocks contract tests (milestone 2 onward), but doesn't block code structure work — develop against fixtures until the key arrives.* |
| 3 | Booking URL pattern? | **Confirmed:** `https://www.hostelworld.com/pwa/hosteldetails.php/{property-slug}/{city-slug}/{property-id}?from=…&to=…&guests=…`. User clicks "Book" on that page → Hostelworld funnels to their own checkout. See [§8](#8-booking-handoff). |
| 4 | Sandbox / staging? | **None.** Contract tests will hit prod with minimal/cached calls and run weekly, not per-commit. |
| 5 | Currency handling? | **Pass-through.** Added `currency` to `search_hostels` and `get_hostel_details` schemas ([§5](#5-tool-api-design)), defaults to USD, model can collect from user. |
| 6 | Multi-tenant scope? | **Public, unofficial MCP** — anyone on the internet can connect. Single shared Hostelworld key. Drives the auth/rate-limit/budget design pivots in §9, §11, §15. |

### 18.2 Still open — resolve before launch

| # | Question | Why it matters | Block |
|---|---|---|---|
| A | **Quota tier — what's our daily/monthly call ceiling?** | Sets the value of "100% budget" in the cap ([§11.4](#114-layer-3-global-budget-cap-new--critical-for-public-model)). Without it, we'd guess. | Blocks final tuning before milestone 11. Code can be written with a placeholder. |
| B | **Affiliate / referral parameter on booking URL?** | If Hostelworld attributes commission via a `?source=partner-X` parameter and we omit it, we lose revenue from day one. | Should be answered with the key request email. Doesn't block code; we can add the param later. |
| C | **Hostelworld TOS — does the partner API allow public-facing search?** | The Partner API is intended for partner properties, not for unaffiliated public-facing tools. Building this and getting access revoked later is a worse outcome than asking now. **Recommend: disclose intent in the email requesting the key.** | Blocks launch (milestone 11) ethically/legally, not technically. |
| D | **OpenAI MCP connector quirks?** | Their hosted client may require specific manifest fields, capability flags, or initialization behavior beyond stock MCP. | Test during milestone 3, before deploy. |
| E | **Slug source — does the property API return a canonical slug?** | If yes, use it directly and skip our slugifier ([§8.4](#84-risks)). If no, contract test the slugifier output against real URLs. | Resolved during milestone 4 once we hit the API. |
| F | **Acceptable monthly upstream-call budget?** | Drives soft/hard cap thresholds and signals when v2 (signup tier) becomes urgent. If Hostelworld's free partner tier is generous, no immediate pressure. If tight, we need v2 sooner. | Settable later; default to "use 100% of whatever quota Hostelworld grants." |

---

## 19. Summary of Key Decisions

| Concern | Choice | Top reason |
|---|---|---|
| **Data source** | **Scrape the public PWA JSON backend** (apigee) | No Partner API key was granted; the PWA backend is stable, typed, and paginated |
| **api-key** | **Bootstrap from the PWA page**, refresh on 401 | It's the PWA's public browser key, not a partner secret; auto-tracks rotation |
| **Upstream resilience** | **One circuit breaker** (`sony/gobreaker`) + low QPS + in-flight cap | Scraped backend can break anytime; fail fast instead of hammering it |
| Transport | Streamable HTTP | OpenAI Responses API speaks it |
| MCP library | mark3labs/mcp-go | Active, spec-current, clean API |
| Tool count | 3 | Matches product flow without action-overloading |
| State for "show more" | Model-tracked `exclude_ids` | Stateless server, naturally conversation-scoped |
| Booking | Deep-link to hostelworld.com | No checkout API to drive; payment stays on their site |
| Server auth | **Open access** | Public unofficial MCP; protection is rate limits + budget cap, not credentials |
| Rate limit | Token bucket per IP + global rate + in-flight cap + daily budget cap | Defends the upstream + our budget since auth doesn't gate access |
| Cache | LRU in-process; no availability cache | Speed without staleness on prices |
| Secrets | Env-loaded config + redacting wrapper type | Keeps the api-key out of logs/responses |
| Deploy | Single Fly.io container behind Cloudflare (mandatory) | Cloudflare is load-bearing for the public model, not optional |
| Currency | Pass-through param, defaults to USD | Backend supports it; let the model collect from user |

---

*End of document. The Partner-API draft was pivoted to live scraping after the key request went unanswered; see the v2 pivot note at the top.*
