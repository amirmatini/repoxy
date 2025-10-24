package proxy

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"repoxy/internal/cache"
	"repoxy/internal/config"
	"repoxy/internal/storage"

	"golang.org/x/net/proxy"
)

// Handler is the main reverse proxy handler
type Handler struct {
	config *config.Config
	store  *cache.Store
	index  *storage.Index
	client *http.Client
}

// New creates a new proxy handler
func New(cfg *config.Config, store *cache.Store, index *storage.Index) *Handler {
	// Custom transport with reasonable timeouts
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second, // Connection timeout
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
	}

	// Configure egress proxy if enabled
	if cfg.Proxy.Enabled {
		if err := configureEgressProxy(transport, &cfg.Proxy); err != nil {
			log.Printf("Warning: failed to configure egress proxy: %v", err)
		} else {
			log.Printf("Egress proxy configured: %s (%s)", cfg.Proxy.URL, cfg.Proxy.Type)
		}
	}

	return &Handler{
		config: cfg,
		store:  store,
		index:  index,
		client: &http.Client{
			Timeout:   5 * time.Minute, // Overall request timeout
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Don't follow redirects
				return http.ErrUseLastResponse
			},
		},
	}
}

// configureEgressProxy sets up the HTTP transport to use an egress proxy
func configureEgressProxy(transport *http.Transport, proxyCfg *config.ProxyConfig) error {
	proxyURL, err := url.Parse(proxyCfg.URL)
	if err != nil {
		return fmt.Errorf("invalid proxy URL: %w", err)
	}

	// Add authentication if provided
	if proxyCfg.Username != "" {
		proxyURL.User = url.UserPassword(proxyCfg.Username, proxyCfg.Password)
	}

	switch strings.ToLower(proxyCfg.Type) {
	case "http", "https":
		// HTTP/HTTPS proxy
		transport.Proxy = http.ProxyURL(proxyURL)

	case "socks5", "socks":
		// SOCKS5 proxy
		var auth *proxy.Auth
		if proxyCfg.Username != "" {
			auth = &proxy.Auth{
				User:     proxyCfg.Username,
				Password: proxyCfg.Password,
			}
		}

		dialer, err := proxy.SOCKS5("tcp", proxyURL.Host, auth, proxy.Direct)
		if err != nil {
			return fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
		}

		transport.Dial = func(network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		}

	default:
		return fmt.Errorf("unsupported proxy type: %s (use 'http' or 'socks5')", proxyCfg.Type)
	}

	return nil
}

// ServeHTTP handles incoming requests
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Reject CONNECT method - we're a reverse proxy, not a forward proxy
	if r.Method == http.MethodConnect {
		http.Error(w, "CONNECT method not supported - this is a reverse proxy, not a forward proxy", http.StatusMethodNotAllowed)
		return
	}

	// Match upstream by path prefix
	repo, upstream, rest := h.config.MatchUpstream(r.URL.Path)
	if upstream == nil {
		http.NotFound(w, r)
		return
	}

	// Build upstream URL
	upstreamURL, err := h.buildUpstreamURL(upstream.BaseURL, rest, r.URL.RawQuery)
	if err != nil {
		log.Printf("proxy: failed to build URL: %v", err)
		http.Error(w, "invalid URL", http.StatusBadRequest)
		return
	}

	// Match policy
	policy := h.config.MatchPolicy(rest)
	if policy == nil {
		log.Printf("proxy: no policy matched for %s", rest)
		http.Error(w, "no policy matched", http.StatusInternalServerError)
		return
	}

	// Generate cache key
	cacheKey := cache.CacheKey(upstreamURL)

	// Check if range request
	rangeHeader := r.Header.Get("Range")

	// Try to serve from cache
	if h.store.Exists(repo, cacheKey) {
		if err := h.serveFromCache(w, r, repo, cacheKey, rest, policy, upstreamURL, *upstream, rangeHeader); err != nil {
			log.Printf("proxy: cache serve error: %v", err)
			http.Error(w, "cache error", http.StatusInternalServerError)
		}
		return
	}

	// Cache miss - acquire lock for request coalescing
	lock, err := h.store.AcquireLock(cacheKey)
	if err != nil {
		log.Printf("proxy: lock timeout for %s", cacheKey)
		http.Error(w, "lock timeout", http.StatusServiceUnavailable)
		return
	}
	// Only release if we actually acquired it
	if lock != nil {
		defer h.store.ReleaseLock(cacheKey)
	}

	// Double-check cache after acquiring lock
	if h.store.Exists(repo, cacheKey) {
		if err := h.serveFromCache(w, r, repo, cacheKey, rest, policy, upstreamURL, *upstream, rangeHeader); err != nil {
			log.Printf("proxy: cache serve error: %v", err)
			http.Error(w, "cache error", http.StatusInternalServerError)
		}
		return
	}

	// Fetch from upstream
	if err := h.fetchAndCache(w, r, repo, cacheKey, rest, policy, upstreamURL, *upstream); err != nil {
		log.Printf("proxy: fetch error: %v", err)
		// Error response already sent by fetchAndCache
	}
}

func (h *Handler) buildUpstreamURL(baseURL, rest, query string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}

	// Safe join
	base.Path = path.Join(base.Path, rest)
	base.RawQuery = query

	return base.String(), nil
}

// updateCacheIndex updates the index after successful caching
func (h *Handler) updateCacheIndex(repo, key string, meta *cache.Metadata) {
	h.index.Put(&storage.IndexEntry{
		Repo:       repo,
		Key:        key,
		URL:        meta.URL,
		Size:       meta.Size,
		LastAccess: meta.LastAccess,
		Hits:       meta.Hits,
	})
}

// applyUpstreamHeaders sets Host header and custom headers for upstream request
func (h *Handler) applyUpstreamHeaders(req *http.Request, upstream config.UpstreamConfig) {
	// Set host header from base_url hostname
	if parsedURL, err := url.Parse(upstream.BaseURL); err == nil && parsedURL.Host != "" {
		req.Host = parsedURL.Host
	}

	// Apply custom headers from upstream config (e.g., Authorization)
	for key, value := range upstream.Headers {
		req.Header.Set(key, value)
	}
}

func (h *Handler) serveFromCache(w http.ResponseWriter, r *http.Request,
	repo, key, rest string, policy *config.PolicyConfig,
	upstreamURL string, upstream config.UpstreamConfig, rangeHeader string) error {

	f, meta, err := h.store.Get(repo, key)
	if err != nil {
		return err
	}
	defer f.Close()

	// Update access stats
	meta.UpdateAccess()
	h.store.UpdateMetadata(repo, key, meta)

	// Update index
	h.updateCacheIndex(repo, key, meta)

	// Check if stale
	isStale := meta.IsStale(policy.CacheTTL)

	// If stale and revalidation enabled, revalidate in background
	if isStale && policy.AllowStaleWhileRevalidate {
		go h.revalidate(repo, key, policy, upstreamURL, upstream, meta)
	}

	// Serve content
	if meta.ContentType != "" {
		w.Header().Set("Content-Type", meta.ContentType)
	}
	w.Header().Set("X-Cache", "HIT")
	w.Header().Set("X-Cache-Policy", policy.Name)
	if isStale {
		w.Header().Set("X-Cache-Status", "STALE")
	} else {
		w.Header().Set("X-Cache-Status", "FRESH")
	}

	// Handle range requests
	if rangeHeader != "" {
		h.serveRange(w, r, f, meta.Size, rangeHeader)
		return nil
	}

	w.Header().Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	w.WriteHeader(http.StatusOK)
	io.Copy(w, f)

	h.index.IncrementStat("hits", 1)
	return nil
}

func (h *Handler) fetchAndCache(w http.ResponseWriter, r *http.Request,
	repo, key, rest string, policy *config.PolicyConfig,
	upstreamURL string, upstream config.UpstreamConfig) error {

	// Create upstream request
	req, err := http.NewRequest("GET", upstreamURL, nil)
	if err != nil {
		return err
	}

	// Apply upstream headers (host + custom headers)
	h.applyUpstreamHeaders(req, upstream)

	// Copy relevant headers from client (but not Range for initial fetch)
	for _, hdr := range []string{"User-Agent", "Accept", "Accept-Encoding"} {
		if val := r.Header.Get(hdr); val != "" {
			req.Header.Set(hdr, val)
		}
	}

	// Fetch
	resp, err := h.client.Do(req)
	if err != nil {
		http.Error(w, "upstream error", http.StatusBadGateway)
		return err
	}
	defer resp.Body.Close()

	// Check if cacheable
	if !h.isCacheable(resp) {
		// Stream through without caching
		h.copyHeaders(w, resp)
		w.Header().Set("X-Cache", "BYPASS")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		h.index.IncrementStat("misses", 1)
		return nil
	}

	// Only cache successful responses
	if resp.StatusCode != http.StatusOK {
		h.copyHeaders(w, resp)
		w.Header().Set("X-Cache", "MISS")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		h.index.IncrementStat("misses", 1)
		return nil
	}

	// Create metadata
	meta := &cache.Metadata{
		URL:          upstreamURL,
		Size:         0, // Will be set by store.Put
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		Policy:       policy.Name,
		CreatedAt:    time.Now(),
		LastAccess:   time.Now(),
		Hits:         1,
		ContentType:  resp.Header.Get("Content-Type"),
	}

	// Use TeeReader to copy to both cache and response
	pr, pw := io.Pipe()
	tee := io.TeeReader(resp.Body, pw)

	// Write to cache in background
	errCh := make(chan error, 1)
	go func() {
		if err := h.store.Put(repo, key, pr, meta); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	// Stream response
	h.copyHeaders(w, resp)
	w.Header().Set("X-Cache", "MISS")
	w.Header().Set("X-Cache-Policy", policy.Name)
	w.WriteHeader(http.StatusOK)
	_, copyErr := io.Copy(w, tee)

	// Close pipe writer to signal EOF to the cache goroutine
	pw.Close()

	// Wait for cache write
	cacheErr := <-errCh

	// If either operation failed, clean up
	if copyErr != nil || cacheErr != nil {
		if copyErr != nil {
			log.Printf("proxy: stream error: %v", copyErr)
		}
		if cacheErr != nil {
			log.Printf("proxy: cache write error: %v", cacheErr)
		}
		// Delete partial/corrupted cache entry
		h.store.Delete(repo, key)
		h.index.IncrementStat("misses", 1)
		return fmt.Errorf("failed to cache: copy=%v, cache=%v", copyErr, cacheErr)
	}

	// Only update index if everything succeeded
	h.updateCacheIndex(repo, key, meta)

	// Create symlink (best effort)
	cache.CreateSymlink(h.config.Cache.Dir, repo, rest, key)

	h.index.IncrementStat("misses", 1)
	return nil
}

func (h *Handler) revalidate(repo, key string, policy *config.PolicyConfig,
	upstreamURL string, upstream config.UpstreamConfig, meta *cache.Metadata) {

	req, err := http.NewRequest("GET", upstreamURL, nil)
	if err != nil {
		log.Printf("revalidate: failed to create request: %v", err)
		return
	}

	// Apply upstream headers (host + custom headers)
	h.applyUpstreamHeaders(req, upstream)

	// Set conditional headers
	if h.config.Cache.RevalidateETag && meta.ETag != "" {
		req.Header.Set("If-None-Match", meta.ETag)
	}
	if h.config.Cache.RevalidateLastMod && meta.LastModified != "" {
		req.Header.Set("If-Modified-Since", meta.LastModified)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		log.Printf("revalidate: request failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		// Still fresh - update metadata
		meta.CreatedAt = time.Now()
		h.store.UpdateMetadata(repo, key, meta)
		log.Printf("revalidate: %s still fresh", upstreamURL)
		return
	}

	if resp.StatusCode == http.StatusOK {
		// Content changed - re-cache
		newMeta := &cache.Metadata{
			URL:          upstreamURL,
			ETag:         resp.Header.Get("ETag"),
			LastModified: resp.Header.Get("Last-Modified"),
			Policy:       policy.Name,
			CreatedAt:    time.Now(),
			LastAccess:   time.Now(),
			Hits:         meta.Hits,
			ContentType:  resp.Header.Get("Content-Type"),
		}

		if err := h.store.Put(repo, key, resp.Body, newMeta); err != nil {
			log.Printf("revalidate: failed to update cache: %v", err)
			return
		}

		h.updateCacheIndex(repo, key, newMeta)
		log.Printf("revalidate: %s updated", upstreamURL)
	}
}

func (h *Handler) isCacheable(resp *http.Response) bool {
	// Check Cache-Control
	cc := resp.Header.Get("Cache-Control")
	if strings.Contains(cc, "no-store") || strings.Contains(cc, "private") {
		return false
	}

	return true
}

func (h *Handler) copyHeaders(w http.ResponseWriter, resp *http.Response) {
	for key, values := range resp.Header {
		// Skip hop-by-hop headers
		if key == "Connection" || key == "Keep-Alive" || key == "Transfer-Encoding" {
			continue
		}
		for _, val := range values {
			w.Header().Add(key, val)
		}
	}
}

func (h *Handler) serveRange(w http.ResponseWriter, r *http.Request, f *os.File, totalSize int64, rangeHeader string) {
	// Parse range header
	ranges, err := parseRange(rangeHeader, totalSize)
	if err != nil || len(ranges) != 1 {
		// Invalid range or multiple ranges not supported
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", totalSize))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}

	rng := ranges[0]

	// Seek and serve
	if _, err := f.Seek(rng.start, 0); err != nil {
		http.Error(w, "seek error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", rng.start, rng.end, totalSize))
	w.Header().Set("Content-Length", strconv.FormatInt(rng.length, 10))
	w.WriteHeader(http.StatusPartialContent)

	io.CopyN(w, f, rng.length)
}

type rangeSpec struct {
	start  int64
	end    int64
	length int64
}

func parseRange(s string, size int64) ([]rangeSpec, error) {
	if !strings.HasPrefix(s, "bytes=") {
		return nil, fmt.Errorf("invalid range header")
	}

	s = strings.TrimPrefix(s, "bytes=")
	parts := strings.Split(s, ",")

	var ranges []rangeSpec
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "-") {
			// Suffix range
			suffix, err := strconv.ParseInt(part[1:], 10, 64)
			if err != nil {
				return nil, err
			}
			if suffix > size {
				suffix = size
			}
			ranges = append(ranges, rangeSpec{
				start:  size - suffix,
				end:    size - 1,
				length: suffix,
			})
		} else {
			pos := strings.Split(part, "-")
			if len(pos) != 2 {
				return nil, fmt.Errorf("invalid range")
			}

			start, err := strconv.ParseInt(pos[0], 10, 64)
			if err != nil {
				return nil, err
			}

			var end int64
			if pos[1] == "" {
				end = size - 1
			} else {
				end, err = strconv.ParseInt(pos[1], 10, 64)
				if err != nil {
					return nil, err
				}
			}

			if start > end || start >= size {
				return nil, fmt.Errorf("invalid range")
			}

			if end >= size {
				end = size - 1
			}

			ranges = append(ranges, rangeSpec{
				start:  start,
				end:    end,
				length: end - start + 1,
			})
		}
	}

	return ranges, nil
}
