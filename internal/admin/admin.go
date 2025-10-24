package admin

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"

	"repoxy/internal/cache"
	"repoxy/internal/config"
	"repoxy/internal/storage"
)

// Handler provides admin API endpoints
type Handler struct {
	config *config.Config
	store  *cache.Store
	index  *storage.Index
}

// New creates a new admin handler
func New(cfg *config.Config, store *cache.Store, index *storage.Index) *Handler {
	return &Handler{
		config: cfg,
		store:  store,
		index:  index,
	}
}

// Health returns a simple health check
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

// Stats returns cache statistics
func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.index.Stats()
	if err != nil {
		http.Error(w, "failed to get stats", http.StatusInternalServerError)
		return
	}

	hits, _ := h.index.GetStat("hits")
	misses, _ := h.index.GetStat("misses")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_size_bytes": stats.TotalSize,
		"total_entries":    stats.TotalCount,
		"total_hits":       hits,
		"total_misses":     misses,
		"hit_ratio":        calculateHitRatio(hits, misses),
	})
}

func calculateHitRatio(hits, misses int64) float64 {
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}

// PurgeByURL purges a specific URL from cache
func (h *Handler) PurgeByURL(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		URL string `json:"url"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.URL == "" {
		http.Error(w, "url is required", http.StatusBadRequest)
		return
	}

	// Find and delete entries with matching URL
	entries, err := h.index.ListAll()
	if err != nil {
		http.Error(w, "failed to list entries", http.StatusInternalServerError)
		return
	}

	var purged int
	for _, entry := range entries {
		if entry.URL == req.URL {
			if err := h.store.Delete(entry.Repo, entry.Key); err != nil {
				log.Printf("admin: failed to delete %s/%s: %v", entry.Repo, entry.Key, err)
				continue
			}

			if err := h.index.Delete(entry.Repo, entry.Key); err != nil {
				log.Printf("admin: failed to delete from index %s/%s: %v", entry.Repo, entry.Key, err)
			}

			purged++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"purged": purged,
	})
}

// PurgeByRegex purges entries matching a regex
func (h *Handler) PurgeByRegex(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Regex string `json:"regex"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.Regex == "" {
		http.Error(w, "regex is required", http.StatusBadRequest)
		return
	}

	// Compile regex
	re, err := regexp.Compile(req.Regex)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid regex: %v", err), http.StatusBadRequest)
		return
	}

	// Find and delete entries with matching URL
	entries, err := h.index.ListAll()
	if err != nil {
		http.Error(w, "failed to list entries", http.StatusInternalServerError)
		return
	}

	var purged int
	for _, entry := range entries {
		if re.MatchString(entry.URL) {
			if err := h.store.Delete(entry.Repo, entry.Key); err != nil {
				log.Printf("admin: failed to delete %s/%s: %v", entry.Repo, entry.Key, err)
				continue
			}

			if err := h.index.Delete(entry.Repo, entry.Key); err != nil {
				log.Printf("admin: failed to delete from index %s/%s: %v", entry.Repo, entry.Key, err)
			}

			purged++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"purged": purged,
	})
}

func (h *Handler) checkAuth(r *http.Request) bool {
	if !h.config.Admin.EnablePurgeAPI {
		return false
	}

	auth := r.Header.Get("Authorization")
	if auth == "" {
		return false
	}

	// Extract token from "Bearer <token>"
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		return false
	}

	return parts[1] == h.config.Admin.Token
}
