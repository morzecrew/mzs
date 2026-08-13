package lru

import (
	"strconv"
	"sync"
	"testing"
)

// The cache backs Options.ProgramCache and Options.RegexCacheSize, so three things
// matter beyond "it stores values": eviction is by least *recent use* and not by
// insertion, a capacity of zero is a permanently empty cache rather than an unbounded
// one, and every operation is safe under the many goroutines a single *Interp serves.

func TestAddAndGet(t *testing.T) {
	c := New[string, int](2)

	if got, ok := c.Get("missing"); ok || got != 0 {
		t.Errorf("Get on an empty cache = (%d, %v); want (0, false)", got, ok)
	}

	c.Add("a", 1)
	c.Add("b", 2)
	if got, ok := c.Get("a"); !ok || got != 1 {
		t.Errorf(`Get("a") = (%d, %v); want (1, true)`, got, ok)
	}
	if c.Len() != 2 {
		t.Errorf("Len = %d; want 2", c.Len())
	}
	if c.Cap() != 2 {
		t.Errorf("Cap = %d; want 2", c.Cap())
	}

	// Re-adding a key replaces the value in place rather than growing the cache.
	c.Add("a", 10)
	if got, _ := c.Get("a"); got != 10 {
		t.Errorf(`Get("a") after re-Add = %d; want 10`, got)
	}
	if c.Len() != 2 {
		t.Errorf("Len after re-Add = %d; want 2", c.Len())
	}
}

// The point of the structure: a Get is a use, so the entry that leaves is the one no
// one has asked for, not the one that arrived first.
func TestEvictsLeastRecentlyUsed(t *testing.T) {
	c := New[string, int](2)
	c.Add("a", 1)
	c.Add("b", 2)

	c.Get("a")    // "a" is now the most recent, "b" the least
	c.Add("c", 3) // evicts "b"

	if _, ok := c.Get("b"); ok {
		t.Error(`"b" survived; the least recently used entry must be the one evicted`)
	}
	for _, k := range []string{"a", "c"} {
		if _, ok := c.Get(k); !ok {
			t.Errorf("%q was evicted; only the least recently used one should be", k)
		}
	}
	if c.Len() != 2 {
		t.Errorf("Len = %d; want the capacity, 2", c.Len())
	}
}

// Eviction has to keep the list ends honest, which only shows up after the head and
// the tail have each been removed at least once.
func TestEvictionKeepsBothEnds(t *testing.T) {
	c := New[int, int](3)
	for i := 0; i < 10; i++ {
		c.Add(i, i*i)
	}
	if c.Len() != 3 {
		t.Fatalf("Len = %d; want 3", c.Len())
	}
	for i := 7; i < 10; i++ {
		if got, ok := c.Get(i); !ok || got != i*i {
			t.Errorf("Get(%d) = (%d, %v); want (%d, true)", i, got, ok, i*i)
		}
	}
	for i := 0; i < 7; i++ {
		if _, ok := c.Get(i); ok {
			t.Errorf("Get(%d) hit; only the last three adds should survive", i)
		}
	}
}

func TestRemove(t *testing.T) {
	c := New[string, int](3)
	c.Add("a", 1)
	c.Add("b", 2)

	if !c.Remove("a") {
		t.Error(`Remove("a") = false; want true`)
	}
	if c.Remove("a") {
		t.Error(`Remove("a") twice = true; the second must report nothing removed`)
	}
	if _, ok := c.Get("a"); ok {
		t.Error(`"a" is still readable after Remove`)
	}
	if c.Len() != 1 {
		t.Errorf("Len = %d; want 1", c.Len())
	}

	// Removing the only entry leaves a cache that still works.
	c.Remove("b")
	c.Add("c", 3)
	if got, ok := c.Get("c"); !ok || got != 3 {
		t.Errorf(`Get("c") after emptying = (%d, %v); want (3, true)`, got, ok)
	}
}

func TestPurge(t *testing.T) {
	c := New[string, int](2)
	c.Add("a", 1)
	c.Add("b", 2)

	c.Purge()
	if c.Len() != 0 {
		t.Errorf("Len after Purge = %d; want 0", c.Len())
	}
	// The list ends are reset too, so the next Add is not linked to a dead entry.
	c.Add("c", 3)
	c.Add("d", 4)
	c.Add("e", 5)
	if got, ok := c.Get("c"); ok {
		t.Errorf(`Get("c") = %d after two later adds; eviction is broken after a Purge`, got)
	}
	if c.Len() != 2 {
		t.Errorf("Len = %d; want 2", c.Len())
	}
}

// A capacity of zero is how Options.ProgramCache == 0 turns caching off: every
// operation is a no-op and Get always misses. A nil receiver behaves the same, so a
// host that never built one is not a nil dereference (A7).
func TestDisabledAndNilCaches(t *testing.T) {
	var nilCache *Cache[string, int]
	for name, c := range map[string]*Cache[string, int]{
		"zero capacity":     New[string, int](0),
		"negative capacity": New[string, int](-1),
		"nil cache":         nilCache,
	} {
		t.Run(name, func(t *testing.T) {
			c.Add("a", 1)
			if got, ok := c.Get("a"); ok || got != 0 {
				t.Errorf("Get = (%d, %v); want (0, false)", got, ok)
			}
			if c.Len() != 0 {
				t.Errorf("Len = %d; want 0", c.Len())
			}
			if c.Remove("a") {
				t.Error("Remove = true; want false")
			}
			c.Purge() // must not panic
		})
	}
	if got := New[string, int](0).Cap(); got != 0 {
		t.Errorf("Cap of a disabled cache = %d; want 0", got)
	}
}

// One *Interp serves any number of concurrent Runs and they share this cache, so the
// mutex has to cover every path. Run with -race for this to mean anything.
func TestConcurrentUse(t *testing.T) {
	c := New[string, int](16)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				k := strconv.Itoa((g*i)%32) + "k"
				c.Add(k, i)
				c.Get(k)
				if i%17 == 0 {
					c.Remove(k)
				}
				if i%97 == 0 {
					c.Purge()
				}
				c.Len()
			}
		}(g)
	}
	wg.Wait()
	if c.Len() > c.Cap() {
		t.Errorf("Len = %d after the storm; the cache grew past its capacity of %d", c.Len(), c.Cap())
	}
}
