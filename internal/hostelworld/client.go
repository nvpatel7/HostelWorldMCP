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
	"time"

	"github.com/nvpatel2002/hostelworld-mcp/internal/cache"
	hwerr "github.com/nvpatel2002/hostelworld-mcp/internal/errors"
	"golang.org/x/time/rate"
)

// Client is the surface tool handlers use. Two implementations: HTTPClient
// (real Partner API) and DemoClient (fixture-backed).
type Client interface {
	Search(ctx context.Context, p SearchParams) (*SearchResult, error)
	Details(ctx context.Context, p DetailsParams) (*PropertyDetail, []Room, error)
}

// HTTPClient calls the real Partner API at partner-api.hostelworld.com.
// Path shapes and field names are best-effort pending live-API verification;
// see DESIGN.md §7 and the // TODO(api): markers below.
type HTTPClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	logger     *slog.Logger
	globalRL   *rate.Limiter

	locationCache *cache.TTL[string, []Location]
	detailCache   *cache.TTL[string, *PropertyDetail]
}

type HTTPConfig struct {
	BaseURL  string
	APIKey   string
	Logger   *slog.Logger
	// GlobalQPS caps total upstream calls per second across all callers.
	GlobalQPS float64
	GlobalBurst int
}

func NewHTTPClient(cfg HTTPConfig) (*HTTPClient, error) {
	locCache, err := cache.New[string, []Location](1000, 24*time.Hour)
	if err != nil {
		return nil, err
	}
	detCache, err := cache.New[string, *PropertyDetail](500, 1*time.Hour)
	if err != nil {
		return nil, err
	}
	return &HTTPClient{
		baseURL:       cfg.BaseURL,
		apiKey:        cfg.APIKey,
		logger:        cfg.Logger,
		httpClient:    &http.Client{Timeout: 15 * time.Second},
		globalRL:      rate.NewLimiter(rate.Limit(cfg.GlobalQPS), cfg.GlobalBurst),
		locationCache: locCache,
		detailCache:   detCache,
	}, nil
}

func (c *HTTPClient) Search(ctx context.Context, p SearchParams) (*SearchResult, error) {
	locID, err := c.resolveCity(ctx, p.City)
	if err != nil {
		return nil, err
	}

	// TODO(api): confirm path + query parameter names against live API.
	q := url.Values{}
	q.Set("location-id", strconv.Itoa(locID))
	q.Set("date-start", p.Checkin)
	q.Set("date-end", p.Checkout)
	q.Set("number-of-guests", strconv.Itoa(p.Guests))
	if p.Currency != "" {
		q.Set("currency", p.Currency)
	}
	if p.Sort != "" {
		q.Set("sort", p.Sort)
	}

	var resp struct {
		Properties []Property `json:"properties"`
		Total      int        `json:"total"`
	}
	if err := c.get(ctx, "/2.2/properties/", q, &resp); err != nil {
		return nil, err
	}

	filtered := filterExcluded(resp.Properties, p.ExcludeIDs)
	if p.Limit > 0 && len(filtered) > p.Limit {
		filtered = filtered[:p.Limit]
	}

	return &SearchResult{Results: filtered, TotalAvailable: resp.Total}, nil
}

func (c *HTTPClient) Details(ctx context.Context, p DetailsParams) (*PropertyDetail, []Room, error) {
	// Static property info is cacheable.
	var detail *PropertyDetail
	if cached, ok := c.detailCache.Get(p.PropertyID); ok {
		detail = cached
	} else {
		var fetched PropertyDetail
		// TODO(api): confirm path.
		if err := c.get(ctx, "/2.2/properties/"+p.PropertyID+"/", nil, &fetched); err != nil {
			return nil, nil, err
		}
		detail = &fetched
		c.detailCache.Set(p.PropertyID, detail)
	}

	// Availability/pricing is never cached (DESIGN.md §12).
	q := url.Values{}
	q.Set("date-start", p.Checkin)
	q.Set("date-end", p.Checkout)
	q.Set("number-of-guests", strconv.Itoa(p.Guests))
	if p.Currency != "" {
		q.Set("currency", p.Currency)
	}

	var av struct {
		Rooms []Room `json:"rooms"`
	}
	// TODO(api): confirm path.
	if err := c.get(ctx, "/2.2/properties/"+p.PropertyID+"/availability/", q, &av); err != nil {
		return nil, nil, err
	}

	return detail, av.Rooms, nil
}

func (c *HTTPClient) resolveCity(ctx context.Context, city string) (int, error) {
	if locs, ok := c.locationCache.Get(city); ok && len(locs) > 0 {
		return locs[0].ID, nil
	}

	q := url.Values{}
	q.Set("query", city)
	var resp struct {
		Locations []Location `json:"locations"`
	}
	if err := c.get(ctx, "/2.2/locations/", q, &resp); err != nil {
		return 0, err
	}
	if len(resp.Locations) == 0 {
		return 0, hwerr.NotFound(fmt.Sprintf("no location found for %q", city))
	}
	c.locationCache.Set(city, resp.Locations)
	return resp.Locations[0].ID, nil
}

func (c *HTTPClient) get(ctx context.Context, path string, q url.Values, out any) error {
	if err := c.globalRL.Wait(ctx); err != nil {
		return hwerr.Wrap(hwerr.CodeServiceBusy, "upstream rate limit wait timed out", err)
	}

	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return hwerr.ServiceError(err)
	}
	// TODO(api): confirm exact header name. Documented as "Api-Key" in the
	// design doc; some Partner APIs use "X-API-Key" or "Authorization".
	req.Header.Set("Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return hwerr.Wrap(hwerr.CodeServiceBusy, "upstream unreachable", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusNotFound {
		return hwerr.NotFound("upstream returned 404")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return &hwerr.Error{
			Code:    hwerr.CodeRateLimited,
			Message: "upstream rate-limited us",
			Retry:   30,
		}
	}
	if resp.StatusCode >= 500 {
		return hwerr.Wrap(hwerr.CodeServiceBusy, "upstream "+resp.Status, nil)
	}
	if resp.StatusCode >= 400 {
		// Don't leak the body — it may contain the key or PII.
		c.logger.Warn("upstream 4xx",
			"status", resp.StatusCode,
			"path", path,
			"body_bytes", len(body),
		)
		return hwerr.New(hwerr.CodeServiceError, "upstream returned "+resp.Status)
	}

	if err := json.Unmarshal(body, out); err != nil {
		return hwerr.Wrap(hwerr.CodeServiceError, "upstream returned malformed JSON", err)
	}
	return nil
}

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
