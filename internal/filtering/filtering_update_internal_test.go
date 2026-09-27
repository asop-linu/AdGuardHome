package filtering

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AdguardTeam/golibs/logutil/slogutil"
	"github.com/AdguardTeam/golibs/testutil"
	"github.com/AdguardTeam/urlfilter/rules"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initTimeout bounds the tests below, which exercise the asynchronous filters
// initialization.
const initTimeout = time.Minute

// newTestFilter returns a conf and a function that writes a filtering-rule file
// of the given size, for use in the initialization tests.
func newTestFilter(t *testing.T) (d *DNSFilter, dataDir string) {
	t.Helper()

	dataDir = t.TempDir()
	c := &Config{
		Logger:                     slogutil.NewDiscardLogger(),
		DataDir:                    dataDir,
		FiltersUpdateIntervalHours: 0,
		BlockingMode:               BlockingModeDefault,
		FilteringEnabled:           true,
	}

	d, err := New(c, nil)
	require.NoError(t, err)

	d.filtersInitializerChan = make(chan filtersInitializerParams, 1)
	d.done = make(chan struct{}, 1)

	return d, dataDir
}

// writeTestFilterFile writes n synthetic host rules and returns the file name to
// use as a [Filter].
func writeTestFilterFile(
	t *testing.T,
	dataDir string,
	id rules.ListID,
	n int,
) (flt Filter) {
	t.Helper()

	data := make([]byte, 0, n*32)
	for i := 0; i < n; i++ {
		data = append(data, fmt.Sprintf("||host%d.example.com^\n", i)...)
	}

	p := filepath.Join(dataDir, filterDir, fmt.Sprintf("%d.txt", id))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, data, 0o644))

	return Filter{ID: id, FilePath: p}
}

// startUpdatesLoop runs d.updatesLoop in the background and returns a function
// that waits for it to return.
func startUpdatesLoop(t *testing.T, d *DNSFilter) (wait func()) {
	t.Helper()

	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)

		d.updatesLoop(context.TODO())
	}()

	return func() {
		t.Helper()

		testutil.RequireReceive(t, loopDone, initTimeout)
	}
}

// TestDNSFilter_close_waitsForInitializer checks that Close does not reset the

// rules storage while an asynchronous initialization is still in progress.  The
// worker would otherwise swap a freshly built engine in, and reopen the
// storage, after it had already been closed.
func TestDNSFilter_close_waitsForInitializer(t *testing.T) {
	d, dataDir := newTestFilter(t)
	wait := startUpdatesLoop(t, d)

	flt := writeTestFilterFile(t, dataDir, 1, 2_000)
	require.NoError(t, d.setFilters(context.TODO(), []Filter{flt}, nil, true))

	// Wait until the initialization is actually under way, so that the test
	// measures the shutdown during the rebuild and not before it.
	awaitFiltersInitializerStart(t, d)

	d.Close()
	wait()

	// Close must have waited for the worker, so nothing may be running anymore.
	assert.False(t, d.filtersInitializerRunning)
	assert.Nil(t, d.filtersInitializerDone)
}

// TestDNSFilter_close_idempotent checks that calling Close more than once is
// safe.  A second Close used to block forever on sending to the already full
// done channel, after the updates loop had returned.
func TestDNSFilter_close_idempotent(t *testing.T) {
	d, dataDir := newTestFilter(t)
	wait := startUpdatesLoop(t, d)

	flt := writeTestFilterFile(t, dataDir, 1, 5_000)
	require.NoError(t, d.setFilters(context.TODO(), []Filter{flt}, nil, true))
	awaitFiltersInitializerStart(t, d)

	// The repeated calls must neither block nor panic, and must not close the
	// rules storage a second time.
	require.NotPanics(t, d.Close)
	require.NotPanics(t, d.Close)
	wait()

	// A final call, now that the updates loop has returned, must also be a
	// no-op rather than a send on a stopped loop.
	require.NotPanics(t, d.Close)
}

// TestDNSFilter_close_unblocksPanickingInitializer checks that a panic inside
// the initialization does not leave Close blocked forever.  Before, the panic
// skipped the bookkeeping, so the done channel was never closed.
func TestDNSFilter_close_unblocksPanickingInitializer(t *testing.T) {
	d, _ := newTestFilter(t)

	// Run the worker without the updates loop, so that it is the only
	// goroutine that can close the channel.
	d.runFiltersInitializer(context.TODO(), filtersInitializerParams{
		blockFilters: []Filter{{ID: 1, FilePath: "no-such-file"}},
	})

	// The invalid filter makes the initialization fail, which must not stop the
	// worker from finishing its bookkeeping.
	d.waitFiltersInitializer()

	assert.False(t, d.filtersInitializerRunning)
	assert.Nil(t, d.filtersInitializerDone)

	// Close must not block either, so run it with a timeout guarantee.
	closed := make(chan struct{})
	go func() {
		defer close(closed)

		d.Close()
	}()

	testutil.RequireReceive(t, closed, initTimeout)
}

// TestDNSFilter_reportsInitError checks that a failed asynchronous rebuild is
// reported, since it leaves the previous, stale engine in place.
func TestDNSFilter_reportsInitError(t *testing.T) {
	d, dataDir := newTestFilter(t)
	t.Cleanup(d.Close)

	// Nothing has run yet, so there is nothing to report.
	assert.NoError(t, d.lastFiltersInitError())

	// Two in-memory lists with the same ID make the rule storage creation fail.
	// A filter with a missing file would not, since such filters are skipped.
	d.runFiltersInitializer(context.TODO(), filtersInitializerParams{
		blockFilters: []Filter{
			{ID: 1, Data: []byte("||dup.example.com^\n")},
			{ID: 1, Data: []byte("||dup2.example.com^\n")},
		},
	})
	d.waitFiltersInitializer()

	assert.Error(t, d.lastFiltersInitError())

	// A subsequent successful rebuild must clear the error.
	flt := writeTestFilterFile(t, dataDir, 1, 10)
	d.runFiltersInitializer(context.TODO(), filtersInitializerParams{
		blockFilters: []Filter{flt},
	})
	d.waitFiltersInitializer()

	assert.NoError(t, d.lastFiltersInitError())
}

// initialization is running, or the timeout expires.
func awaitFiltersInitializerStart(t *testing.T, d *DNSFilter) {
	t.Helper()

	exp := time.Now().Add(initTimeout)
	for time.Now().Before(exp) {
		d.filtersInitializerLock.Lock()
		running := d.filtersInitializerRunning
		d.filtersInitializerLock.Unlock()

		if running {
			return
		}

		time.Sleep(time.Millisecond)
	}

	t.Fatal("filters initialization did not start")
}

// TestDNSFilter_updatesLoop_appliesLatestRequest checks that a request that
// arrives while another initialization is in progress is not lost.  It
// simulates removing a filtering list, which must eventually result in an
// engine without any lists.
func TestDNSFilter_updatesLoop_appliesLatestRequest(t *testing.T) {
	d, dataDir := newTestFilter(t)
	wait := startUpdatesLoop(t, d)

	flt := writeTestFilterFile(t, dataDir, 1, 5_000)

	// Queue the rebuild, then immediately queue its removal.  The latter must
	// win, otherwise the deleted list stays active.
	require.NoError(t, d.setFilters(context.TODO(), []Filter{flt}, nil, true))
	require.NoError(t, d.setFilters(context.TODO(), nil, nil, true))

	// Make sure the initialization is actually under way first, and then wait
	// for it, including the request that supersedes it.
	awaitFiltersInitializerStart(t, d)
	d.waitFiltersInitializer()

	require.NotNil(t, d.filteringEngine)

	_, matched := d.filteringEngine.Match("host0.example.com")
	assert.False(t, matched)

	d.Close()
	wait()
}

// TestDNSFilter_updatesLoop_closeWhileInitializing checks that the updates loop
// stays responsive to the shutdown signal while an initialization is in
// progress.  Before, the initialization ran inline and the shutdown signal could
// not be handled until it had finished.
func TestDNSFilter_updatesLoop_closeWhileInitializing(t *testing.T) {
	d, dataDir := newTestFilter(t)
	wait := startUpdatesLoop(t, d)

	// Queue an expensive rebuild and immediately request the shutdown.
	flt := writeTestFilterFile(t, dataDir, 1, 20_000)
	require.NoError(t, d.setFilters(context.TODO(), []Filter{flt}, nil, true))

	awaitFiltersInitializerStart(t, d)

	d.Close()
	wait()
}

// TestDNSFilter_setFilters_removesPending checks that a newer asynchronous
// request discards the one that has not been started yet, so that only the most
// recent configuration is applied.
func TestDNSFilter_setFilters_removesPending(t *testing.T) {
	d, _ := newTestFilter(t)
	t.Cleanup(d.Close)

	// Queue two requests without letting the loop consume them, so that the
	// second one must supersede the first.
	require.NoError(t, d.setFilters(context.TODO(), []Filter{{ID: 1}}, nil, true))
	require.NoError(t, d.setFilters(context.TODO(), []Filter{{ID: 2}}, nil, true))

	// Only the newest request must be pending.
	assert.Equal(t, 1, len(d.filtersInitializerChan))

	got := <-d.filtersInitializerChan
	assert.Equal(t, rules.ListID(2), got.blockFilters[0].ID)
}
