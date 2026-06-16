# Deploying to Railway (for use with OpenAI)

This server is a public, unofficial Hostelworld MCP. OpenAI's Responses API connects to it
*server-to-server* over HTTPS, so all we need in production is a public `https://<host>/mcp`
endpoint with TLS. Railway gives us both (TLS-terminated public domain + a built image).

## 1. Prerequisites

- A Railway account and the project pushed to a Git repo (GitHub) or the Railway CLI installed.
- The repo already contains everything Railway needs:
  - [`Dockerfile`](Dockerfile) — static binary in a distroless image.
  - [`railway.json`](railway.json) — builds the Dockerfile, health-checks `/healthz`, single replica.
  - [`.dockerignore`](.dockerignore) — keeps the image lean.

> **Single replica matters.** The caches, rate limiter, circuit breaker, and daily budget counter
> are all in-process. Scaling to multiple replicas would split that state (each replica its own
> budget/limits). Keep `numReplicas: 1` until we add shared state. See [DESIGN.md §15.3](DESIGN.md#153-why-not-kubernetes--multi-region-for-v1).

## 2. Create the service

**From GitHub:** New Project → Deploy from GitHub repo → pick this repo. Railway detects
`railway.json` + `Dockerfile` and builds.

**Or from the CLI:**
```bash
npm i -g @railway/cli
railway login
railway init        # in the repo root
railway up          # build + deploy
```

## 3. Set environment variables

In the service's **Variables** tab (or `railway variables set KEY=VALUE`):

| Variable | Value | Why |
|---|---|---|
| `HOSTELWORLD_DEMO` | `false` | Use the live scrape, not fixtures. **Required** — without it the repo's default `.env` habit could leave you in demo mode. |
| `DAILY_BUDGET` | e.g. `5000` | Daily upstream-call ceiling before the hard cap. Tune to taste. |

You do **not** need to set `LISTEN_ADDR` or `PORT` — Railway injects `PORT`, and the app binds
`0.0.0.0:$PORT` automatically (see `config.listenAddr`). You also don't need any Hostelworld
credentials: the api-key is the PWA's public key and is scraped/refreshed at runtime.

Optional:
- `HOSTELWORLD_APIGEE_KEY` — pin the api-key instead of scraping it (rarely needed).
- `BREAKER_MAX_FAILURES` / `BREAKER_COOLDOWN_SECS`, `GLOBAL_QPS`, `MAX_IN_FLIGHT` — tuning knobs.
- `REAL_IP_HEADER` — only if you put Cloudflare in front (see §6). Leave unset on plain Railway.

## 4. Expose a public domain

Service → **Settings → Networking → Generate Domain** (or attach a custom domain). Railway
terminates TLS for you. Your MCP URL is:

```
https://<your-app>.up.railway.app/mcp
```

## 5. Verify it's live

```bash
curl https://<your-app>.up.railway.app/healthz          # -> ok

# Full MCP handshake + a real search:
BASE=https://<your-app>.up.railway.app/mcp
CT="Content-Type: application/json"; AC="Accept: application/json, text/event-stream"
SID=$(curl -s -i -X POST $BASE -H "$CT" -H "$AC" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}' \
  | grep -i '^Mcp-Session-Id:' | tr -d '\r' | awk '{print $2}')
curl -s -o /dev/null -X POST $BASE -H "$CT" -H "$AC" -H "Mcp-Session-Id: $SID" \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'
curl -s -X POST $BASE -H "$CT" -H "$AC" -H "Mcp-Session-Id: $SID" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_hostels","arguments":{"city":"Amsterdam","checkin":"2026-07-15","checkout":"2026-07-18","guests":2,"currency":"EUR"}}}'
```

## 6. (Optional) Cloudflare in front

For edge DDoS/bot protection and real per-IP rate limiting, put Cloudflare in front of the Railway
domain and set `REAL_IP_HEADER=CF-Connecting-IP`. Without Cloudflare the per-IP limiter keys on
Railway's ingress IP — acceptable for low traffic, but it can't distinguish callers. See
[DESIGN.md §15.4](DESIGN.md#154-in-front-of-the-server-non-optional-for-public-model).

## 7. Connect from OpenAI

Add the deployed server as an MCP tool in the Responses API:

```python
from openai import OpenAI
client = OpenAI()

resp = client.responses.create(
    model="gpt-4.1",
    tools=[{
        "type": "mcp",
        "server_label": "hostelworld",
        "server_url": "https://<your-app>.up.railway.app/mcp",
        "require_approval": "never",   # or "always" to gate each tool call
    }],
    input="Find me a hostel in Amsterdam for 2 people, July 15-18, then give me a booking link for the top one.",
)
print(resp.output_text)
```

OpenAI performs the MCP handshake (`initialize` → `tools/list` → `tools/call`) for you. No auth
headers are needed because the endpoint is open by design.

## Operational notes

- **Budget counter resets on redeploy.** `budget.json` lives on the container filesystem, which is
  ephemeral on Railway — a new deploy starts the daily count at zero. For a single long-running
  service that's usually fine; attach a Railway **Volume** and point `BUDGET_FILE` at it if you want
  the count to survive deploys.
- **Watch the logs for `circuit breaker state change ... to=open`.** That's your signal the scrape
  broke upstream (key rotation or endpoint drift). The api-key auto-refreshes on 401; if the breaker
  keeps reopening, re-capture the apigee response shapes.
- **Kill switch.** There's no built-in `SERVICE_DISABLED` yet (it's in DESIGN.md §15.5 as a TODO);
  for now, pause/scale the Railway service to take it offline fast.
