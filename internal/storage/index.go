package storage

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	entriesBucket = []byte("entries")
	statsBucket   = []byte("stats")
)

// IndexEntry represents an entry in the LRU index
type IndexEntry struct {
	Repo       string    `json:"repo"`
	Key        string    `json:"key"`
	URL        string    `json:"url"`
	Size       int64     `json:"size"`
	LastAccess time.Time `json:"last_access"`
	Hits       int64     `json:"hits"`
}

// Index manages the BoltDB-based LRU index
type Index struct {
	db *bolt.DB
}

// NewIndex creates or opens a BoltDB index
func NewIndex(path string) (*Index, error) {
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open index: %w", err)
	}

	// Initialize buckets
	if err := db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(entriesBucket); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(statsBucket); err != nil {
			return err
		}
		return nil
	}); err != nil {
		db.Close()
		return nil, err
	}

	return &Index{db: db}, nil
}

// Close closes the index
func (idx *Index) Close() error {
	return idx.db.Close()
}

// Count returns the number of entries in the index
func (idx *Index) Count() (int, error) {
	var count int
	err := idx.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(entriesBucket)
		count = b.Stats().KeyN
		return nil
	})
	return count, err
}

// Put adds or updates an entry in the index
func (idx *Index) Put(entry *IndexEntry) error {
	return idx.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(entriesBucket)

		key := []byte(entry.Repo + "/" + entry.Key)
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}

		return b.Put(key, data)
	})
}

// Get retrieves an entry from the index
func (idx *Index) Get(repo, key string) (*IndexEntry, error) {
	var entry IndexEntry

	err := idx.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(entriesBucket)

		k := []byte(repo + "/" + key)
		data := b.Get(k)
		if data == nil {
			return fmt.Errorf("entry not found")
		}

		return json.Unmarshal(data, &entry)
	})

	if err != nil {
		return nil, err
	}

	return &entry, nil
}

// Delete removes an entry from the index
func (idx *Index) Delete(repo, key string) error {
	return idx.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(entriesBucket)
		k := []byte(repo + "/" + key)
		return b.Delete(k)
	})
}

// TotalSize calculates the total cached size
func (idx *Index) TotalSize() (int64, error) {
	var total int64

	err := idx.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(entriesBucket)

		return b.ForEach(func(k, v []byte) error {
			var entry IndexEntry
			if err := json.Unmarshal(v, &entry); err != nil {
				return nil // Skip corrupt entries
			}
			total += entry.Size
			return nil
		})
	})

	return total, err
}

// ListByLRU returns entries sorted by last access (oldest first)
func (idx *Index) ListByLRU(limit int) ([]*IndexEntry, error) {
	var entries []*IndexEntry

	err := idx.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(entriesBucket)

		return b.ForEach(func(k, v []byte) error {
			var entry IndexEntry
			if err := json.Unmarshal(v, &entry); err != nil {
				return nil // Skip corrupt entries
			}
			entries = append(entries, &entry)
			return nil
		})
	})

	if err != nil {
		return nil, err
	}

	// Sort by last access (oldest first)
	// Simple insertion sort for small lists
	for i := 1; i < len(entries); i++ {
		j := i
		for j > 0 && entries[j].LastAccess.Before(entries[j-1].LastAccess) {
			entries[j], entries[j-1] = entries[j-1], entries[j]
			j--
		}
	}

	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}

	return entries, nil
}

// ListAll returns all entries
func (idx *Index) ListAll() ([]*IndexEntry, error) {
	var entries []*IndexEntry

	err := idx.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(entriesBucket)

		return b.ForEach(func(k, v []byte) error {
			var entry IndexEntry
			if err := json.Unmarshal(v, &entry); err != nil {
				return nil // Skip corrupt entries
			}
			entries = append(entries, &entry)
			return nil
		})
	})

	return entries, err
}

// Stats returns cache statistics
func (idx *Index) Stats() (*Stats, error) {
	totalSize, err := idx.TotalSize()
	if err != nil {
		return nil, err
	}

	count, err := idx.Count()
	if err != nil {
		return nil, err
	}

	var totalHits int64
	err = idx.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(entriesBucket)

		return b.ForEach(func(k, v []byte) error {
			var entry IndexEntry
			if err := json.Unmarshal(v, &entry); err != nil {
				return nil
			}
			totalHits += entry.Hits
			return nil
		})
	})

	if err != nil {
		return nil, err
	}

	return &Stats{
		TotalSize:  totalSize,
		TotalCount: count,
		TotalHits:  totalHits,
	}, nil
}

// IncrementStat increments a named counter
func (idx *Index) IncrementStat(name string, delta int64) error {
	return idx.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(statsBucket)

		key := []byte(name)
		val := b.Get(key)

		var current int64
		if val != nil && len(val) == 8 {
			current = int64(binary.BigEndian.Uint64(val))
		}

		current += delta

		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, uint64(current))

		return b.Put(key, buf)
	})
}

// GetStat retrieves a named counter
func (idx *Index) GetStat(name string) (int64, error) {
	var value int64

	err := idx.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(statsBucket)
		val := b.Get([]byte(name))

		if val != nil && len(val) == 8 {
			value = int64(binary.BigEndian.Uint64(val))
		}

		return nil
	})

	return value, err
}

// Stats represents cache statistics
type Stats struct {
	TotalSize  int64 `json:"total_size"`
	TotalCount int   `json:"total_count"`
	TotalHits  int64 `json:"total_hits"`
}
