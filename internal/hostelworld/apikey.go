package hostelworld

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"

	hwerr "github.com/nvpatel2002/hostelworld-mcp/internal/errors"
)

// The apigee api-key is not a secret we own — it is the public key the
// Hostelworld PWA ships to every browser, embedded in the page's Nuxt runtime
// config as APIGEE_KEY:"...". We bootstrap it by fetching the PWA page and
// extracting it, so we automatically track Hostelworld's key rotations. A
// hardcoded fallback (last-known-good) lets the server start even if the page
// fetch fails; the env override lets an operator pin a key. See DESIGN.md §9.

// fallbackAPIKey is a last-known-good key, used only if page extraction and the
// env override both fail. Treated as best-effort, not authoritative.
const fallbackAPIKey = "cvFkm2A4AAefXoupLsChH4jL2mA2VGSyEA0MkRUrqz8Z8x5H"

var apigeeKeyRe = regexp.MustCompile(`APIGEE_KEY:"([A-Za-z0-9]+)"`)

// keyProvider lazily fetches and caches the PWA's apigee key. Safe for
// concurrent use.
type keyProvider struct {
	pageURL   string
	override  string // from HOSTELWORLD_APIGEE_KEY; pins the key if set
	ttl       time.Duration
	http      *http.Client
	userAgent string
	logger    *slog.Logger

	mu        sync.Mutex
	key       string
	fetchedAt time.Time
}

func newKeyProvider(pageURL, override, userAgent string, ttl time.Duration, httpClient *http.Client, logger *slog.Logger) *keyProvider {
	return &keyProvider{
		pageURL:   pageURL,
		override:  override,
		ttl:       ttl,
		http:      httpClient,
		userAgent: userAgent,
		logger:    logger,
	}
}

// Get returns a usable api-key, fetching and caching it from the PWA page on
// first use and after the TTL expires.
func (k *keyProvider) Get(ctx context.Context) (string, error) {
	if k.override != "" {
		return k.override, nil
	}

	k.mu.Lock()
	if k.key != "" && time.Since(k.fetchedAt) < k.ttl {
		key := k.key
		k.mu.Unlock()
		return key, nil
	}
	k.mu.Unlock()

	key, err := k.fetch(ctx)
	if err != nil {
		// Fall back to the last-known-good key (cached, then compiled-in) so a
		// transient page outage doesn't take the whole service down.
		k.mu.Lock()
		cached := k.key
		k.mu.Unlock()
		if cached != "" {
			k.logger.Warn("apigee key refresh failed; using cached key", "err", err)
			return cached, nil
		}
		k.logger.Warn("apigee key fetch failed; using compiled-in fallback", "err", err)
		return fallbackAPIKey, nil
	}

	k.mu.Lock()
	k.key = key
	k.fetchedAt = time.Now()
	k.mu.Unlock()
	return key, nil
}

// Invalidate forces the next Get to re-fetch. Called when the upstream rejects
// the current key (401), in case Hostelworld rotated it.
func (k *keyProvider) Invalidate() {
	k.mu.Lock()
	k.fetchedAt = time.Time{}
	k.mu.Unlock()
}

func (k *keyProvider) fetch(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.pageURL, nil)
	if err != nil {
		return "", hwerr.ServiceError(err)
	}
	req.Header.Set("User-Agent", k.userAgent)
	req.Header.Set("Accept", "text/html")

	resp, err := k.http.Do(req)
	if err != nil {
		return "", hwerr.Wrap(hwerr.CodeServiceBusy, "PWA page unreachable", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", hwerr.New(hwerr.CodeServiceError, "PWA page returned "+resp.Status)
	}

	// Cap the read; the key lives in the runtime config near the top, but pages
	// are large. 2 MiB is plenty and bounds memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", hwerr.ServiceError(err)
	}

	m := apigeeKeyRe.FindSubmatch(body)
	if m == nil {
		return "", hwerr.New(hwerr.CodeServiceError, "APIGEE_KEY not found in PWA page")
	}
	return string(m[1]), nil
}
