package meta

import (
	"encoding/json"
	"sync"
	"time"
)

// CacheTTL is how long a provider response stays fresh.
const CacheTTL = 14 * 24 * time.Hour

// CacheStore is the persistence side of the provider cache. *store.DB
// implements it against the provider_cache table ((provider, key) ->
// response, fetched_at).
type CacheStore interface {
	GetCached(provider, key string) (response string, fetchedAtMs int64, ok bool)
	PutCached(provider, key, response string) error
}

var (
	cacheMu    sync.RWMutex
	cacheStore CacheStore
	memCache   = map[string]memEntry{}
)

type memEntry struct {
	val       string
	expiresMs int64
}

// SetCacheStore binds the cache backend; nil disables persistence (an
// in-process map keeps dedupe semantics for tests and keyless runs).
// Idempotent.
func SetCacheStore(cs CacheStore) {
	cacheMu.Lock()
	cacheStore = cs
	cacheMu.Unlock()
}

// ResetCache drops the in-process cache (test isolation).
func ResetCache() {
	cacheMu.Lock()
	memCache = map[string]memEntry{}
	cacheMu.Unlock()
}

// CacheGet returns the raw cached response for (provider, key) when present
// and younger than CacheTTL.
func CacheGet(provider, key string) (string, bool) {
	cacheMu.RLock()
	cs := cacheStore
	cacheMu.RUnlock()
	if cs == nil {
		cacheMu.Lock()
		e, ok := memCache[provider+"\x00"+key]
		cacheMu.Unlock()
		if !ok || time.Now().UnixMilli() > e.expiresMs {
			return "", false
		}
		return e.val, true
	}
	resp, at, ok := cs.GetCached(provider, key)
	if !ok || time.Now().UnixMilli()-at > CacheTTL.Milliseconds() {
		return "", false
	}
	return resp, true
}

// CacheGetJSON decodes a fresh cached response into out; reports a hit.
func CacheGetJSON(provider, key string, out any) bool {
	resp, ok := CacheGet(provider, key)
	if !ok {
		return false
	}
	return json.Unmarshal([]byte(resp), out) == nil
}

// CachePut stores v as JSON under (provider, key). Persists via the bound
// store when present; always mirrors into the in-process map.
func CachePut(provider, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	cacheMu.Lock()
	memCache[provider+"\x00"+key] = memEntry{val: string(b), expiresMs: time.Now().UnixMilli() + CacheTTL.Milliseconds()}
	cacheMu.Unlock()
	cacheMu.RLock()
	cs := cacheStore
	cacheMu.RUnlock()
	if cs == nil {
		return nil
	}
	return cs.PutCached(provider, key, string(b))
}
