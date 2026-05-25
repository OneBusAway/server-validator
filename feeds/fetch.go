package feeds

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Fetcher downloads feeds. Static feeds go through the conditional-GET Cache;
// realtime feeds are always fetched fresh.
type Fetcher struct {
	http    *http.Client
	cache   *Cache
	noCache bool
	refresh bool
}

func NewFetcher(httpClient *http.Client, cache *Cache, noCache, refresh bool) *Fetcher {
	return &Fetcher{http: httpClient, cache: cache, noCache: noCache, refresh: refresh}
}

func (f *Fetcher) get(ctx context.Context, url string, hdr http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	return f.http.Do(req)
}

// FetchRealtime always performs a fresh GET (no caching).
func (f *Fetcher) FetchRealtime(ctx context.Context, url string) ([]byte, error) {
	resp, err := f.get(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// FetchStatic returns the static feed bytes, using the conditional-GET cache.
func (f *Fetcher) FetchStatic(ctx context.Context, url string) ([]byte, error) {
	if f.noCache || f.cache == nil {
		return f.FetchRealtime(ctx, url)
	}
	k := key(url)
	mu := f.cache.lockFor(k)
	mu.Lock()
	defer mu.Unlock()

	body, meta, hit := f.cache.load(k)
	if hit && !f.refresh && meta.fresh(f.cache.ttl) {
		return body, nil
	}

	hdr := http.Header{}
	if hit && !f.refresh {
		if meta.ETag != "" {
			hdr.Set("If-None-Match", meta.ETag)
		}
		if meta.LastModified != "" {
			hdr.Set("If-Modified-Since", meta.LastModified)
		}
	}
	resp, err := f.get(ctx, url, hdr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified && hit {
		return body, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	newBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// Cache writes are best-effort: a write failure must not fail the fetch,
	// because we already hold valid bytes. (No logger in this package.)
	_ = f.cache.store(k, newBody, cacheMeta{
		URL:          url,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		FetchedAt:    time.Now(),
	})
	return newBody, nil
}
