// Package lru is the bounded cache used for compiled programs (Options.ProgramCache)
// and for runtime-compiled regexes (Options.RegexCacheSize). Both are shared by many
// goroutines through a single *Interp, so every operation takes the mutex.
package lru

import "sync"

type entry[K comparable, V any] struct {
	key  K
	val  V
	cost int64
	prev *entry[K, V]
	next *entry[K, V]
}

// Cache is a fixed-capacity least-recently-used map. The zero value is not usable;
// call New. A cache created with n <= 0 is permanently empty: Get always misses and
// Add is a no-op, which is how a negative Options.ProgramCache disables caching — it
// normalizes to 0, and 0 reaches here only from that path.
type Cache[K comparable, V any] struct {
	mu  sync.Mutex
	cap int
	// maxCost, when positive, also bounds the sum of the costs of the entries: what a
	// cache of compiled regexes holds is bytes, and a count alone does not bound it.
	maxCost int64
	cost    int64
	m       map[K]*entry[K, V]
	head    *entry[K, V] // most recently used
	tail    *entry[K, V] // least recently used
}

func New[K comparable, V any](n int) *Cache[K, V] {
	c := &Cache[K, V]{cap: n}
	if n > 0 {
		c.m = make(map[K]*entry[K, V], n)
	}
	return c
}

// NewCost is New with a second bound: the entries' costs, as given to AddCost, never
// sum past maxCost. An entry that costs more than maxCost on its own is not kept.
func NewCost[K comparable, V any](n int, maxCost int64) *Cache[K, V] {
	c := New[K, V](n)
	c.maxCost = maxCost
	return c
}

// Cost reports the sum of the costs of the entries held.
func (c *Cache[K, V]) Cost() int64 {
	if c == nil || c.cap <= 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cost
}

// Cap reports the configured capacity; <= 0 means the cache is disabled.
func (c *Cache[K, V]) Cap() int { return c.cap }

func (c *Cache[K, V]) Len() int {
	if c == nil || c.cap <= 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
	var zero V
	if c == nil || c.cap <= 0 {
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok {
		return zero, false
	}
	c.unlink(e)
	c.pushFront(e)
	return e.val, true
}

func (c *Cache[K, V]) Add(key K, val V) { c.AddCost(key, val, 0) }

// AddCost is Add for an entry that costs cost against the cache's maxCost. The least
// recently used entries are dropped until both bounds hold.
func (c *Cache[K, V]) AddCost(key K, val V, cost int64) {
	if c == nil || c.cap <= 0 {
		return
	}
	if cost < 0 {
		cost = 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[key]; ok {
		c.unlink(e)
		delete(c.m, key)
		c.cost -= e.cost
	}
	if c.maxCost > 0 && cost > c.maxCost {
		return
	}
	for len(c.m) > 0 && (len(c.m) >= c.cap || (c.maxCost > 0 && c.cost+cost > c.maxCost)) {
		old := c.tail
		c.unlink(old)
		delete(c.m, old.key)
		c.cost -= old.cost
	}
	e := &entry[K, V]{key: key, val: val, cost: cost}
	c.m[key] = e
	c.cost += cost
	c.pushFront(e)
}

// Remove drops key if present; it reports whether anything was removed.
func (c *Cache[K, V]) Remove(key K) bool {
	if c == nil || c.cap <= 0 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok {
		return false
	}
	c.unlink(e)
	delete(c.m, key)
	c.cost -= e.cost
	return true
}

func (c *Cache[K, V]) Purge() {
	if c == nil || c.cap <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m = make(map[K]*entry[K, V], c.cap)
	c.head, c.tail = nil, nil
	c.cost = 0
}

func (c *Cache[K, V]) pushFront(e *entry[K, V]) {
	e.prev = nil
	e.next = c.head
	if c.head != nil {
		c.head.prev = e
	}
	c.head = e
	if c.tail == nil {
		c.tail = e
	}
}

func (c *Cache[K, V]) unlink(e *entry[K, V]) {
	if e.prev != nil {
		e.prev.next = e.next
	} else if c.head == e {
		c.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else if c.tail == e {
		c.tail = e.prev
	}
	e.prev, e.next = nil, nil
}
