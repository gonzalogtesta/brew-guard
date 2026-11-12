package dates

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Cache stores found dates on disk. A version's first publication date never
// changes, so entries do not expire. Unknown results are never cached.
type Cache struct {
	path string
	mu   sync.Mutex
	m    map[string]Result
}

// OpenCache loads path, or starts empty if it is missing or unreadable.
func OpenCache(path string) *Cache {
	c := &Cache{path: path, m: map[string]Result{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &c.m)
	}
	return c
}

func (c *Cache) Get(key string) (Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.m[key]
	return r, ok && r.Known()
}

func (c *Cache) Put(key string, r Result) {
	if !r.Known() {
		return
	}
	c.mu.Lock()
	c.m[key] = r
	c.mu.Unlock()
}

// Save writes the cache with owner-only permissions.
func (c *Cache) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c.m, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}
