package janitor

import (
	"log"
	"time"

	"repoxy/internal/cache"
	"repoxy/internal/storage"
)

// Janitor performs periodic cleanup and eviction
type Janitor struct {
	store    *cache.Store
	index    *storage.Index
	maxSize  int64
	interval time.Duration
	stopCh   chan struct{}
}

// New creates a new janitor
func New(store *cache.Store, index *storage.Index, maxSize int64, interval time.Duration) *Janitor {
	return &Janitor{
		store:    store,
		index:    index,
		maxSize:  maxSize,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the janitor loop
func (j *Janitor) Start() {
	go j.run()
}

// Stop stops the janitor
func (j *Janitor) Stop() {
	close(j.stopCh)
}

func (j *Janitor) run() {
	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()

	// Run immediately on start
	j.cleanup()

	for {
		select {
		case <-ticker.C:
			j.cleanup()
		case <-j.stopCh:
			return
		}
	}
}

func (j *Janitor) cleanup() {
	totalSize, err := j.index.TotalSize()
	if err != nil {
		log.Printf("janitor: failed to get total size: %v", err)
		return
	}

	if totalSize <= j.maxSize {
		return
	}

	log.Printf("janitor: cache size %d exceeds max %d, evicting...", totalSize, j.maxSize)

	// Get entries sorted by LRU
	entries, err := j.index.ListByLRU(0)
	if err != nil {
		log.Printf("janitor: failed to list entries: %v", err)
		return
	}

	var evicted int
	var freedBytes int64

	for _, entry := range entries {
		if totalSize-freedBytes <= j.maxSize {
			break
		}

		// Delete from disk
		if err := j.store.Delete(entry.Repo, entry.Key); err != nil {
			log.Printf("janitor: failed to delete %s/%s: %v", entry.Repo, entry.Key, err)
			continue
		}

		// Delete from index
		if err := j.index.Delete(entry.Repo, entry.Key); err != nil {
			log.Printf("janitor: failed to delete from index %s/%s: %v", entry.Repo, entry.Key, err)
		}

		evicted++
		freedBytes += entry.Size
	}

	if evicted > 0 {
		log.Printf("janitor: evicted %d entries, freed %d bytes", evicted, freedBytes)
	}
}

// EvictStale removes entries that haven't been accessed in the given TTL
func (j *Janitor) EvictStale(inactiveTTL time.Duration) error {
	entries, err := j.index.ListAll()
	if err != nil {
		return err
	}

	now := time.Now()
	var evicted int

	for _, entry := range entries {
		if now.Sub(entry.LastAccess) > inactiveTTL {
			if err := j.store.Delete(entry.Repo, entry.Key); err != nil {
				log.Printf("janitor: failed to delete stale %s/%s: %v", entry.Repo, entry.Key, err)
				continue
			}

			if err := j.index.Delete(entry.Repo, entry.Key); err != nil {
				log.Printf("janitor: failed to delete stale from index %s/%s: %v", entry.Repo, entry.Key, err)
			}

			evicted++
		}
	}

	if evicted > 0 {
		log.Printf("janitor: evicted %d stale entries", evicted)
	}

	return nil
}
