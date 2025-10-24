package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Metadata represents the sidecar JSON for each cached object
type Metadata struct {
	URL          string    `json:"url"`
	Size         int64     `json:"size"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	Policy       string    `json:"policy"`
	CreatedAt    time.Time `json:"created_at"`
	LastAccess   time.Time `json:"last_access"`
	Hits         int64     `json:"hits"`
	ContentType  string    `json:"content_type,omitempty"`
}

// CacheKey generates a SHA256 hash for the cache key
func CacheKey(url string) string {
	h := sha256.Sum256([]byte(url))
	return hex.EncodeToString(h[:])
}

// BlobPath returns the path to the cached blob
func BlobPath(cacheDir, repo, key string) string {
	return filepath.Join(cacheDir, repo, key, "blob")
}

// MetadataPath returns the path to the metadata file
func MetadataPath(cacheDir, repo, key string) string {
	return filepath.Join(cacheDir, repo, key, "meta.json")
}

// SymlinkPath returns the human-readable symlink path
func SymlinkPath(cacheDir, repo, rest string) string {
	return filepath.Join(cacheDir, "by-path", repo, rest)
}

// LoadMetadata reads metadata from disk
func LoadMetadata(path string) (*Metadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var meta Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}

	return &meta, nil
}

// SaveMetadata writes metadata to disk
func SaveMetadata(path string, meta *Metadata) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Write to temporary file and rename for atomicity
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
}

// CreateSymlink creates a best-effort symlink from by-path to the blob
func CreateSymlink(cacheDir, repo, rest, key string) error {
	linkPath := SymlinkPath(cacheDir, repo, rest)
	blobPath := BlobPath(cacheDir, repo, key)

	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(linkPath), 0755); err != nil {
		return err
	}

	// Remove existing symlink if present
	_ = os.Remove(linkPath)

	// Calculate relative path for the symlink
	relPath, err := filepath.Rel(filepath.Dir(linkPath), blobPath)
	if err != nil {
		return err
	}

	// Best effort - ignore errors
	_ = os.Symlink(relPath, linkPath)
	return nil
}

// IsStale checks if cached content is stale based on TTL
func (m *Metadata) IsStale(ttl time.Duration) bool {
	return time.Since(m.CreatedAt) > ttl
}

// UpdateAccess updates access time and hit counter
func (m *Metadata) UpdateAccess() {
	m.LastAccess = time.Now()
	m.Hits++
}

// Entry represents a cache entry with both metadata and file info
type Entry struct {
	Key      string
	Repo     string
	Metadata *Metadata
	BlobPath string
}

// Remove deletes both blob and metadata
func (e *Entry) Remove() error {
	var firstErr error

	if err := os.Remove(e.BlobPath); err != nil && !os.IsNotExist(err) {
		firstErr = err
	}

	// Remove meta.json (sibling to blob)
	metaDir := filepath.Dir(e.BlobPath)
	metaPath := filepath.Join(metaDir, "meta.json")
	if err := os.Remove(metaPath); err != nil && !os.IsNotExist(err) && firstErr == nil {
		firstErr = err
	}

	// Try to remove the directory if it's empty
	os.Remove(metaDir)

	return firstErr
}

// String returns a string representation of the entry
func (e *Entry) String() string {
	return fmt.Sprintf("Entry{repo=%s, key=%s, url=%s, size=%d}",
		e.Repo, e.Key, e.Metadata.URL, e.Metadata.Size)
}
