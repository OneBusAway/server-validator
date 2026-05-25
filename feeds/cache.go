package feeds

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type cacheMeta struct {
	URL          string    `json:"url"`
	ETag         string    `json:"etag"`
	LastModified string    `json:"lastModified"`
	FetchedAt    time.Time `json:"fetchedAt"`
}

// fresh reports whether a *validator-less* entry is still within ttl. Entries
// that carry an ETag/Last-Modified are always revalidated, never TTL-served.
func (m *cacheMeta) fresh(ttl time.Duration) bool {
	return m.ETag == "" && m.LastModified == "" && time.Since(m.FetchedAt) < ttl
}

// Cache is an on-disk conditional-GET cache for static feeds.
type Cache struct {
	dir   string
	ttl   time.Duration
	locks sync.Map // key -> *sync.Mutex
}

func NewCache(dir string, ttl time.Duration) *Cache {
	return &Cache{dir: dir, ttl: ttl}
}

func key(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}

func (c *Cache) lockFor(k string) *sync.Mutex {
	m, _ := c.locks.LoadOrStore(k, &sync.Mutex{})
	return m.(*sync.Mutex)
}

func (c *Cache) bodyPath(k string) string { return filepath.Join(c.dir, k+".body") }
func (c *Cache) metaPath(k string) string { return filepath.Join(c.dir, k+".meta.json") }

// load returns the entry only if BOTH body and parseable meta exist; a body
// without valid meta (e.g. truncated write) is reported as a miss.
func (c *Cache) load(k string) ([]byte, *cacheMeta, bool) {
	body, err := os.ReadFile(c.bodyPath(k))
	if err != nil {
		return nil, nil, false
	}
	mb, err := os.ReadFile(c.metaPath(k))
	if err != nil {
		return nil, nil, false
	}
	var m cacheMeta
	if err := json.Unmarshal(mb, &m); err != nil {
		return nil, nil, false
	}
	return body, &m, true
}

// store writes body via temp-file + atomic rename, THEN writes meta, so a crash
// can never leave a valid meta pointing at a partial body.
func (c *Cache) store(k string, body []byte, m cacheMeta) error {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.dir, k+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, c.bodyPath(k)); err != nil {
		os.Remove(tmpName)
		return err
	}
	mb, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(c.metaPath(k), mb, 0o644)
}
