package limit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/cache"
	contractscache "github.com/gradii/framework/contracts/cache"
	"github.com/gradii/framework/foundation/json"
	configmock "github.com/gradii/framework/mocks/config"
)

// memoryCache is the memory driver seen as a cache.Cache, the way the limiter
// receives it from the container.
type memoryCache struct {
	*cache.Memory
}

func (r memoryCache) Store(string) contractscache.Driver {
	return r
}

// TestStoreWithMemoryKeepsLimiting runs the limiter against the real memory
// driver. The bucket is stored with an expiration and the lock is taken on
// every request, so an expiration that outlives the value it belongs to lets
// requests through: the bucket is dropped mid-interval and the next request
// starts from a full one.
//
// The admitted requests are grouped by the window the limiter itself reports -
// the reset timestamp its Take returns - not by a wall-clock round of the test.
// The limiter anchors its window to the first request, the test anchors its
// clock to the moment it started, and the two drift apart by however long the
// first Take takes. On a loaded machine one window can stretch sparse, leaving
// tokens in the previous tick that are then spent at the top of the next test
// round; a round that straddles two limiter windows counts a request from each
// and can exceed the limit without the limiter ever admitting more than its
// share. Grouping by the reported reset removes that alignment from the
// assertion and pins the real invariant: at most `tokens` per limiter window.
func TestStoreWithMemoryKeepsLimiting(t *testing.T) {
	const (
		tokens   = 20
		interval = 300 * time.Millisecond
		rounds   = 4
	)

	mockConfig := configmock.NewConfig(t)
	mockConfig.EXPECT().GetString("cache.prefix").Return("goravel_cache")
	memory, err := cache.NewMemory(mockConfig)
	require.NoError(t, err)

	store := NewStore(memoryCache{memory}, json.New(), tokens, interval)

	admitted := make(map[uint64]int)
	total := 0
	var firstReset uint64
	start := time.Now()
	for time.Since(start) < rounds*interval {
		_, _, reset, ok, err := store.Take(context.Background(), "127.0.0.1")
		require.NoError(t, err)
		if ok {
			if total == 0 {
				firstReset = reset
			}
			admitted[reset]++
			total++
		}

		time.Sleep(interval / 60)
	}

	for reset, count := range admitted {
		assert.LessOrEqual(t, count, tokens, "window resetting at %d let %d requests through, the limit is %d", reset, count, tokens)
	}

	// The limiter anchors its window to the first request and must keep that
	// anchor: every reset it reports is that anchor advanced by whole intervals,
	// so all resets share one phase modulo the interval. A bucket that was
	// dropped and recreated mid-run - the expiration regression this test
	// guards - gets a fresh anchor and shifts the phase, admitting a second full
	// window inside the same interval. The per-window count above cannot see
	// that, because the fabricate-a-window-per-request limiter reports a
	// perfectly legal count for each window it invents.
	for reset := range admitted {
		assert.Zerof(t, (reset-firstReset)%uint64(interval), "window reset %d is not on the anchor's phase (first reset %d, interval %d): the window anchor moved", reset, firstReset, interval)
	}

	// A limiter that refuses every request also stays under the limit, so pin
	// that it admits at all over the run.
	require.NotZero(t, total, "the limiter let nothing through")
}
