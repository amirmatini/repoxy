package cache

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store manages cached objects on disk
type Store struct {
	cacheDir string
	locks    *LockManager
}

// NewStore creates a new cache store
func NewStore(cacheDir string, lockTimeout time.Duration) (*Store, error) {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache dir: %w", err)
	}

	return &Store{
		cacheDir: cacheDir,
		locks:    NewLockManager(lockTimeout),
	}, nil
}

// Get retrieves a cached object and its metadata
func (s *Store) Get(repo, key string) (*os.File, *Metadata, error) {
	blobPath := BlobPath(s.cacheDir, repo, key)
	metaPath := MetadataPath(s.cacheDir, repo, key)

	// Load metadata
	meta, err := LoadMetadata(metaPath)
	if err != nil {
		return nil, nil, err
	}

	// Open blob
	f, err := os.Open(blobPath)
	if err != nil {
		return nil, nil, err
	}

	return f, meta, nil
}

// Put stores a new cached object with metadata
func (s *Store) Put(repo, key string, reader io.Reader, meta *Metadata) error {
	blobPath := BlobPath(s.cacheDir, repo, key)
	metaPath := MetadataPath(s.cacheDir, repo, key)

	// Create key directory (contains blob and meta.json)
	keyDir := filepath.Join(s.cacheDir, repo, key)
	if err := os.MkdirAll(keyDir, 0755); err != nil {
		return fmt.Errorf("failed to create key dir: %w", err)
	}

	// Write blob to temporary file
	tmpPath := blobPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer f.Close()

	written, err := io.Copy(f, reader)
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write blob: %w", err)
	}

	if err := f.Sync(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to sync blob: %w", err)
	}
	f.Close()

	// Update metadata with actual size
	meta.Size = written

	// Atomically rename to final location
	if err := os.Rename(tmpPath, blobPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename blob: %w", err)
	}

	// Save metadata
	if err := SaveMetadata(metaPath, meta); err != nil {
		// Clean up blob on metadata save failure
		os.Remove(blobPath)
		return fmt.Errorf("failed to save metadata: %w", err)
	}

	return nil
}

// UpdateMetadata updates just the metadata file
func (s *Store) UpdateMetadata(repo, key string, meta *Metadata) error {
	metaPath := MetadataPath(s.cacheDir, repo, key)
	return SaveMetadata(metaPath, meta)
}

// Exists checks if a cache entry exists
func (s *Store) Exists(repo, key string) bool {
	blobPath := BlobPath(s.cacheDir, repo, key)
	metaPath := MetadataPath(s.cacheDir, repo, key)

	_, err1 := os.Stat(blobPath)
	_, err2 := os.Stat(metaPath)

	return err1 == nil && err2 == nil
}

// Delete removes a cache entry
func (s *Store) Delete(repo, key string) error {
	blobPath := BlobPath(s.cacheDir, repo, key)
	metaPath := MetadataPath(s.cacheDir, repo, key)

	var firstErr error

	if err := os.Remove(blobPath); err != nil && !os.IsNotExist(err) {
		firstErr = err
	}

	if err := os.Remove(metaPath); err != nil && !os.IsNotExist(err) && firstErr == nil {
		firstErr = err
	}

	return firstErr
}

// AcquireLock acquires a lock for the given key (for request coalescing)
func (s *Store) AcquireLock(key string) (*sync.Mutex, error) {
	return s.locks.Acquire(key)
}

// ReleaseLock releases a lock for the given key
func (s *Store) ReleaseLock(key string) {
	s.locks.Release(key)
}

// LockManager manages per-key locks for request coalescing
type LockManager struct {
	mu      sync.Mutex
	locks   map[string]*sync.Mutex
	timeout time.Duration
}

// NewLockManager creates a new lock manager
func NewLockManager(timeout time.Duration) *LockManager {
	return &LockManager{
		locks:   make(map[string]*sync.Mutex),
		timeout: timeout,
	}
}

// Acquire gets or creates a lock for the key
func (lm *LockManager) Acquire(key string) (*sync.Mutex, error) {
	lm.mu.Lock()
	lock, exists := lm.locks[key]
	if !exists {
		lock = &sync.Mutex{}
		lm.locks[key] = lock
	}
	lm.mu.Unlock()

	// Try to acquire with timeout
	done := make(chan struct{})
	go func() {
		lock.Lock()
		close(done)
	}()

	select {
	case <-done:
		return lock, nil
	case <-time.After(lm.timeout):
		// Timeout - remove from map since we didn't acquire
		lm.mu.Lock()
		delete(lm.locks, key)
		lm.mu.Unlock()
		return nil, fmt.Errorf("lock timeout")
	}
}

// Release unlocks and cleans up the lock
func (lm *LockManager) Release(key string) {
	lm.mu.Lock()
	lock, exists := lm.locks[key]
	if exists {
		delete(lm.locks, key)
		lm.mu.Unlock()
		lock.Unlock()
	} else {
		lm.mu.Unlock()
	}
}
