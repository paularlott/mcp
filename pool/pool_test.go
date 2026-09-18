package pool

import (
	"net/http"
	"sync"
	"testing"
)

// resetPoolForTest clears the package-level singleton state before a test
// and restores whatever was there afterward, so tests don't leak state into
// each other or into the rest of the test binary (these are process-global
// vars with no production reset method — reaching into them directly is only
// safe from within this package's own tests).
func resetPoolForTest(t *testing.T) {
	t.Helper()

	defaultPoolMu.Lock()
	prevPool := defaultPool
	defaultPool = nil
	defaultPoolMu.Unlock()

	poolConfigMutex.Lock()
	prevConfig := poolConfig
	poolConfig = nil
	poolConfigMutex.Unlock()

	t.Cleanup(func() {
		defaultPoolMu.Lock()
		defaultPool = prevPool
		defaultPoolMu.Unlock()

		poolConfigMutex.Lock()
		poolConfig = prevConfig
		poolConfigMutex.Unlock()
	})
}

type fakePool struct{ client *http.Client }

func (f *fakePool) GetHTTPClient() *http.Client { return f.client }

func TestGetPool_ReturnsSingleton(t *testing.T) {
	resetPoolForTest(t)

	p1 := GetPool()
	p2 := GetPool()
	if p1 != p2 {
		t.Fatalf("expected GetPool to return the same instance, got %p and %p", p1, p2)
	}
	if p1 == nil {
		t.Fatal("expected a non-nil default pool")
	}
}

func TestSetPool_InjectedBeforeGetPoolIsRespected(t *testing.T) {
	resetPoolForTest(t)

	injected := &fakePool{client: &http.Client{}}
	SetPool(injected)

	got := GetPool()
	if got != injected {
		t.Fatalf("expected GetPool to return the injected pool, got %v", got)
	}
}

func TestSetPool_AfterGetPoolOverridesIt(t *testing.T) {
	resetPoolForTest(t)

	first := GetPool() // builds and caches the default pool
	injected := &fakePool{client: &http.Client{}}
	SetPool(injected)

	got := GetPool()
	if got != injected {
		t.Fatal("expected SetPool to override a previously-built default pool")
	}
	if got == first {
		t.Fatal("expected the default pool built by the first GetPool call to no longer be returned")
	}
}

// TestGetPool_ConcurrentCallsReturnSameInstance is the direct regression test
// for the race this package used to have: GetPool() gated building the
// default pool behind a plain, unsynchronized `if defaultPool == nil` check
// in front of a sync.Once — a classic broken double-checked-locking pattern
// that -race caught the moment something (RemoteProvider.GetTools, once it
// started running per-server fetches concurrently) called GetPool() from
// multiple goroutines for the first time. Every goroutine here must observe
// the exact same *DefaultPool, and the run must be race-free under -race.
func TestGetPool_ConcurrentCallsReturnSameInstance(t *testing.T) {
	resetPoolForTest(t)

	const goroutines = 50
	results := make([]HTTPPool, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = GetPool()
		}(i)
	}
	wg.Wait()

	first := results[0]
	if first == nil {
		t.Fatal("expected a non-nil pool")
	}
	for i, p := range results {
		if p != first {
			t.Fatalf("goroutine %d got a different pool instance than goroutine 0 (%p vs %p) — the default pool was built more than once", i, p, first)
		}
	}
}

// TestSetPool_RaceWithFirstGetPool races SetPool against many concurrent
// first-ever GetPool calls. This does NOT assert every GetPool call returns
// the same instance: SetPool always overwrites unconditionally, at any time
// (TestSetPool_AfterGetPoolOverridesIt confirms that's intended), so a
// GetPool call truly concurrent with SetPool — with nothing else
// synchronizing them — can legitimately observe either the freshly-built
// default pool or the injected one depending on ordering. An earlier version
// of this test asserted they must all match and failed intermittently; that
// was this test's bug, not the package's — there is no ordering guarantee to
// assert there. What IS guaranteed, and what this test checks: no data race
// (run with -race), no panic, and every individual call returns a valid,
// non-nil pool.
func TestSetPool_RaceWithFirstGetPool(t *testing.T) {
	resetPoolForTest(t)

	injected := &fakePool{client: &http.Client{}}

	const goroutines = 50
	results := make([]HTTPPool, goroutines)
	var wg sync.WaitGroup
	wg.Add(1 + goroutines)
	go func() {
		defer wg.Done()
		SetPool(injected)
	}()
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			results[i] = GetPool()
		}(i)
	}
	wg.Wait()

	for i, p := range results {
		if p == nil {
			t.Fatalf("goroutine %d got a nil pool", i)
		}
	}
}

func TestGetPoolConfig_DefaultsWhenUnset(t *testing.T) {
	resetPoolForTest(t)

	got := GetPoolConfig()
	want := *DefaultPoolConfig()
	if got != want {
		t.Fatalf("GetPoolConfig() = %+v, want defaults %+v", got, want)
	}
}

func TestSetPoolConfig_ThenGetPoolUsesIt(t *testing.T) {
	resetPoolForTest(t)

	SetPoolConfig(&PoolConfig{InsecureSkipVerify: true, MaxIdleConns: 7})
	got := GetPoolConfig()
	if !got.InsecureSkipVerify || got.MaxIdleConns != 7 {
		t.Fatalf("GetPoolConfig() = %+v, want InsecureSkipVerify=true MaxIdleConns=7", got)
	}

	// newDefaultPoolImpl (invoked by GetPool) must pick up this config, not
	// silently fall back to DefaultPoolConfig().
	p := GetPool()
	dp, ok := p.(*DefaultPool)
	if !ok {
		t.Fatalf("expected *DefaultPool, got %T", p)
	}
	transport, ok := dp.GetHTTPClient().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", dp.GetHTTPClient().Transport)
	}
	if !transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("expected the configured InsecureSkipVerify to reach the transport")
	}
	if transport.MaxIdleConns != 7 {
		t.Errorf("MaxIdleConns = %d, want 7", transport.MaxIdleConns)
	}
}

func TestNewPool_MergesDefaultsForZeroFields(t *testing.T) {
	p := NewPool(&PoolConfig{InsecureSkipVerify: true})
	dp, ok := p.(*DefaultPool)
	if !ok {
		t.Fatalf("expected *DefaultPool, got %T", p)
	}
	transport, ok := dp.GetHTTPClient().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", dp.GetHTTPClient().Transport)
	}

	defaults := DefaultPoolConfig()
	if !transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=true to be preserved")
	}
	if transport.MaxIdleConns != defaults.MaxIdleConns {
		t.Errorf("MaxIdleConns = %d, want default %d", transport.MaxIdleConns, defaults.MaxIdleConns)
	}
	if transport.MaxIdleConnsPerHost != defaults.MaxIdleConnsPerHost {
		t.Errorf("MaxIdleConnsPerHost = %d, want default %d", transport.MaxIdleConnsPerHost, defaults.MaxIdleConnsPerHost)
	}
	if transport.IdleConnTimeout != defaults.IdleConnTimeout {
		t.Errorf("IdleConnTimeout = %v, want default %v", transport.IdleConnTimeout, defaults.IdleConnTimeout)
	}
}

func TestNewPool_PreservesNonZeroFields(t *testing.T) {
	p := NewPool(&PoolConfig{MaxIdleConns: 3, MaxIdleConnsPerHost: 4})
	dp := p.(*DefaultPool)
	transport := dp.GetHTTPClient().Transport.(*http.Transport)

	if transport.MaxIdleConns != 3 {
		t.Errorf("MaxIdleConns = %d, want 3", transport.MaxIdleConns)
	}
	if transport.MaxIdleConnsPerHost != 4 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 4", transport.MaxIdleConnsPerHost)
	}
}
