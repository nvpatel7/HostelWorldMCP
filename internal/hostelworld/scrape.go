package hostelworld

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nvpatel2002/hostelworld-mcp/internal/breaker"
	"github.com/nvpatel2002/hostelworld-mcp/internal/cache"
	hwerr "github.com/nvpatel2002/hostelworld-mcp/internal/errors"
	"golang.org/x/time/rate"
)

// Client is the surface tool handlers use. Two implementations: ScrapeClient
// (the live public-site backend) and DemoClient (fixture-backed).
type Client interface {
	Search(ctx context.Context, p SearchParams) (*SearchResult, error)
	Details(ctx context.Context, p DetailsParams) (*PropertyDetail, []Room, error)
}

// Upstream service paths on the apigee host. These back Hostelworld's public
// PWA; we call them directly with the api-key the PWA ships (see apikey.go).
// This is an unofficial integration, not a contracted Partner API — hence the
// circuit breaker and conservative rate limits around every call.
const (
	hwapiPath        = "/legacy-hwapi-service/2.2"
	autocompletePath = "/autocomplete-service/v1/autocomplete/web"
)

// ScrapeClient talks to the apigee JSON backend behind www.hostelworld.com.
type ScrapeClient struct {
	base      string
	userAgent string
	http      *http.Client
	keys      *keyProvider
	logger    *slog.Logger

	globalRL *rate.Limiter
	sem      chan struct{} // caps concurrent in-flight upstream requests
	breaker  *breaker.Breaker

	cityCache   *cache.TTL[string, int]             // city name → city id (24h)
	detailCache *cache.TTL[string, *PropertyDetail] // property static info (1h)
}

type ScrapeConfig struct {
	// APIGeeBaseURL is the apigee host, e.g. https://prod.apigee.hostelworld.com.
	APIGeeBaseURL string
	// PWAPageURL is a search page used to bootstrap the api-key.
	PWAPageURL string
	// APIGeeKey, if set, pins the api-key and skips page scraping.
	APIGeeKey   string
	UserAgent   string
	Logger      *slog.Logger
	GlobalQPS   float64
	GlobalBurst int
	// MaxInFlight caps concurrent upstream requests (politeness).
	MaxInFlight int
	Breaker     *breaker.Breaker
}

func NewScrapeClient(cfg ScrapeConfig) (*ScrapeClient, error) {
	cityCache, err := cache.New[string, int](2000, 24*time.Hour)
	if err != nil {
		return nil, err
	}
	detailCache, err := cache.New[string, *PropertyDetail](500, 1*time.Hour)
	if err != nil {
		return nil, err
	}
	if cfg.MaxInFlight <= 0 {
		cfg.MaxInFlight = 4
	}
	httpClient := &http.Client{Timeout: 15 * time.Second}

	return &ScrapeClient{
		base:        strings.TrimRight(cfg.APIGeeBaseURL, "/"),
		userAgent:   cfg.UserAgent,
		http:        httpClient,
		keys:        newKeyProvider(cfg.PWAPageURL, cfg.APIGeeKey, cfg.UserAgent, 6*time.Hour, httpClient, cfg.Logger),
		logger:      cfg.Logger,
		globalRL:    rate.NewLimiter(rate.Limit(cfg.GlobalQPS), cfg.GlobalBurst),
		sem:         make(chan struct{}, cfg.MaxInFlight),
		breaker:     cfg.Breaker,
		cityCache:   cityCache,
		detailCache: detailCache,
	}, nil
}

func (c *ScrapeClient) Search(ctx context.Context, p SearchParams) (*SearchResult, error) {
	cityID, err := c.resolveCity(ctx, p.City)
	if err != nil {
		return nil, err
	}
	nights, err := numNights(p.Checkin, p.Checkout)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("number-of-guests", strconv.Itoa(p.Guests))
	q.Set("date-start", p.Checkin)
	q.Set("num-nights", strconv.Itoa(nights))
	q.Set("page", "1")
	q.Set("application", "web")
	if p.Currency != "" {
		q.Set("currency", p.Currency)
	}
	// NOTE: the upstream exposes its own sort order; we don't forward p.Sort
	// because the exact query param is unconfirmed and a wrong value 400s.

	var resp apigeeCityProperties
	if err := c.getJSON(ctx, hwapiPath+"/cities/"+strconv.Itoa(cityID)+"/properties/", q, &resp); err != nil {
		return nil, err
	}

	props := make([]Property, 0, len(resp.Properties))
	for _, ap := range resp.Properties {
		props = append(props, ap.toProperty())
	}
	props = filterExcluded(props, p.ExcludeIDs)
	if p.Limit > 0 && len(props) > p.Limit {
		props = props[:p.Limit]
	}

	total := resp.Pagination.Total
	if total == 0 {
		total = len(props)
	}
	return &SearchResult{Results: props, TotalAvailable: total}, nil
}

func (c *ScrapeClient) Details(ctx context.Context, p DetailsParams) (*PropertyDetail, []Room, error) {
	detail, err := c.fetchDetail(ctx, p.PropertyID)
	if err != nil {
		return nil, nil, err
	}

	nights, err := numNights(p.Checkin, p.Checkout)
	if err != nil {
		return nil, nil, err
	}
	q := url.Values{}
	q.Set("date-start", p.Checkin)
	q.Set("num-nights", strconv.Itoa(nights))
	q.Set("number-of-guests", strconv.Itoa(p.Guests))
	q.Set("application", "web")
	if p.Currency != "" {
		q.Set("currency", p.Currency)
	}

	var av apigeeAvailability
	if err := c.getJSON(ctx, hwapiPath+"/properties/"+p.PropertyID+"/availability/", q, &av); err != nil {
		return nil, nil, err
	}
	return detail, av.toRooms(), nil
}

// fetchDetail returns property static info, served from cache when warm.
func (c *ScrapeClient) fetchDetail(ctx context.Context, propertyID string) (*PropertyDetail, error) {
	if d, ok := c.detailCache.Get(propertyID); ok {
		return d, nil
	}
	q := url.Values{}
	q.Set("application", "web")

	var ad apigeeDetail
	if err := c.getJSON(ctx, hwapiPath+"/properties/"+propertyID+"/", q, &ad); err != nil {
		return nil, err
	}
	d := ad.toDetail()
	c.detailCache.Set(propertyID, d)
	return d, nil
}

// resolveCity maps a city name to Hostelworld's city id via the autocomplete
// service. Cached aggressively (cities don't move). A name with no match is a
// not_found, not an upstream failure.
func (c *ScrapeClient) resolveCity(ctx context.Context, city string) (int, error) {
	key := strings.ToLower(strings.TrimSpace(city))
	if id, ok := c.cityCache.Get(key); ok {
		return id, nil
	}

	q := url.Values{}
	q.Set("text", city)

	body, err := c.getRaw(ctx, autocompletePath, q)
	if err != nil {
		return 0, err
	}

	// No match returns a plain-text apology with 200; that unmarshal fails and
	// we treat it as not_found rather than a service error.
	var sugg []apigeeSuggestion
	if err := json.Unmarshal(body, &sugg); err != nil || len(sugg) == 0 {
		return 0, hwerr.NotFound(fmt.Sprintf("no location found for %q", city))
	}

	id := sugg[0].ID
	for _, s := range sugg {
		if s.Type == "city" {
			id = s.ID
			break
		}
	}
	c.cityCache.Set(key, id)
	return id, nil
}

// getJSON fetches and unmarshals an apigee endpoint into out.
func (c *ScrapeClient) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	body, err := c.getRaw(ctx, path, q)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return hwerr.Wrap(hwerr.CodeServiceError, "upstream returned malformed JSON", err)
	}
	return nil
}

// getRaw performs one logical upstream operation under the circuit breaker:
// rate-limit wait, concurrency cap, request with retry/backoff on 429/5xx, and
// a single key refresh on 401. Business outcomes (404 → not_found) are returned
// without tripping the breaker; transport/5xx failures count toward tripping it.
func (c *ScrapeClient) getRaw(ctx context.Context, path string, q url.Values) ([]byte, error) {
	full := c.base + path
	if len(q) > 0 {
		full += "?" + q.Encode()
	}

	var body []byte
	var business error
	execErr := c.breaker.Execute(func() error {
		b, biz, err := c.fetchWithRetry(ctx, path, full)
		body, business = b, biz
		return err
	})
	if execErr != nil {
		return nil, execErr
	}
	return body, business
}

const maxAttempts = 3

// fetchWithRetry returns (body, businessErr, execErr). Exactly one is non-nil
// for body/businessErr on success paths; execErr is set only for failures that
// should count against the breaker.
func (c *ScrapeClient) fetchWithRetry(ctx context.Context, path, full string) ([]byte, error, error) {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, nil, hwerr.Wrap(hwerr.CodeServiceBusy, "request cancelled during backoff", ctx.Err())
			case <-time.After(backoff(attempt)):
			}
		}
		if err := c.globalRL.Wait(ctx); err != nil {
			return nil, nil, hwerr.Wrap(hwerr.CodeServiceBusy, "upstream rate limit wait timed out", err)
		}

		status, body, err := c.attempt(ctx, full)
		if err != nil {
			lastErr = hwerr.Wrap(hwerr.CodeServiceBusy, "upstream unreachable", err)
			continue
		}

		switch {
		case status == http.StatusOK:
			return body, nil, nil
		case status == http.StatusUnauthorized:
			// Key may have rotated; force a refresh and retry.
			c.keys.Invalidate()
			lastErr = hwerr.New(hwerr.CodeServiceError, "upstream rejected api-key")
			continue
		case status == http.StatusNotFound:
			return nil, hwerr.NotFound("upstream returned 404"), nil
		case status == http.StatusTooManyRequests:
			lastErr = &hwerr.Error{Code: hwerr.CodeRateLimited, Message: "upstream rate-limited us", Retry: 30}
			continue
		case status >= 500:
			lastErr = hwerr.Wrap(hwerr.CodeServiceBusy, "upstream "+strconv.Itoa(status), nil)
			continue
		default:
			// Other 4xx — bad params, etc. Don't leak the body (key/PII hygiene).
			c.logger.Warn("upstream 4xx", "status", status, "path", path, "body_bytes", len(body))
			return nil, nil, hwerr.New(hwerr.CodeServiceError, "upstream returned "+strconv.Itoa(status))
		}
	}
	if lastErr == nil {
		lastErr = hwerr.New(hwerr.CodeServiceBusy, "upstream failed after retries")
	}
	return nil, nil, lastErr
}

// attempt performs a single HTTP request, holding an in-flight slot.
func (c *ScrapeClient) attempt(ctx context.Context, full string) (int, []byte, error) {
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}

	key, err := c.keys.Get(ctx)
	if err != nil {
		return 0, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("api-key", key)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, body, nil
}

// backoff is exponential with a small base: ~150ms, 300ms, 600ms.
func backoff(attempt int) time.Duration {
	return time.Duration(150*(1<<(attempt-1))) * time.Millisecond
}

// numNights returns the night count between two YYYY-MM-DD dates.
func numNights(checkin, checkout string) (int, error) {
	ci, err := time.Parse("2006-01-02", checkin)
	if err != nil {
		return 0, hwerr.InvalidInput("checkin must be YYYY-MM-DD")
	}
	co, err := time.Parse("2006-01-02", checkout)
	if err != nil {
		return 0, hwerr.InvalidInput("checkout must be YYYY-MM-DD")
	}
	n := int(co.Sub(ci).Hours() / 24)
	if n < 1 {
		return 0, hwerr.InvalidInput("checkout must be after checkin")
	}
	return n, nil
}

// filterExcluded drops properties whose IDs the model has already shown.
func filterExcluded(props []Property, exclude []string) []Property {
	if len(exclude) == 0 {
		return props
	}
	excl := make(map[string]struct{}, len(exclude))
	for _, id := range exclude {
		excl[id] = struct{}{}
	}
	out := props[:0]
	for _, p := range props {
		if _, skip := excl[p.ID]; skip {
			continue
		}
		out = append(out, p)
	}
	return out
}
