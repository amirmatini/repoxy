package storage

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"repoxy/internal/cache"
)

// RebuildFromDisk scans the cache directory and rebuilds the index from existing meta.json files
func (idx *Index) RebuildFromDisk(cacheDir string) error {
	log.Println("Scanning cache directory for existing files...")

	var scanned, added int

	// Walk through cache directory
	err := filepath.Walk(cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors, continue scanning
		}

		// Only process meta.json files
		if info.IsDir() || !strings.HasSuffix(path, "meta.json") {
			return nil
		}

		scanned++

		// Load metadata
		meta, err := cache.LoadMetadata(path)
		if err != nil {
			log.Printf("rebuild: failed to load %s: %v", path, err)
			return nil // Skip this file, continue
		}

		// Extract repo and key from path
		// Path format: {cacheDir}/{repo}/{sha256}/meta.json
		relPath, err := filepath.Rel(cacheDir, path)
		if err != nil {
			return nil
		}

		parts := strings.Split(filepath.ToSlash(relPath), "/")
		if len(parts) < 3 {
			return nil // Invalid path structure
		}

		repo := parts[0]
		key := parts[1]

		// Check if blob exists
		blobPath := cache.BlobPath(cacheDir, repo, key)
		if _, err := os.Stat(blobPath); os.IsNotExist(err) {
			log.Printf("rebuild: blob missing for %s/%s, skipping", repo, key)
			return nil
		}

		// Add to index
		entry := &IndexEntry{
			Repo:       repo,
			Key:        key,
			URL:        meta.URL,
			Size:       meta.Size,
			LastAccess: meta.LastAccess,
			Hits:       meta.Hits,
		}

		if err := idx.Put(entry); err != nil {
			log.Printf("rebuild: failed to add %s/%s to index: %v", repo, key, err)
			return nil
		}

		added++

		if added%100 == 0 {
			log.Printf("rebuild: processed %d files...", added)
		}

		return nil
	})

	if err != nil {
		return err
	}

	log.Printf("Index rebuild complete: scanned %d metadata files, added %d entries", scanned, added)
	return nil
}
