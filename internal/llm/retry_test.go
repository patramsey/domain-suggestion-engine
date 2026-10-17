package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func countingServer(t *testing.T, status int, header map[string]string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		w.Write([]byte(`{"error":{"code":` + http.StatusText(status) + `}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// Errors that cannot succeed on a second try must not be retried: a bad key
// (400), depleted credits (402) or a permission error (403).
func TestNoRetryOnClientErrors(t *testing.T) {
	for _, code := range []int{400, 402, 403} {
		srv, n := countingServer(t, code, nil)
		c := NewClient("k", "m")
		c.BaseURL = srv.URL + "/"
		if _, err := c.callWithRetry(context.Background(), "s", "u"); err == nil {
			t.Fatalf("%d: want an error", code)
		}
		if got := n.Load(); got != 1 {
			t.Errorf("%d: %d attempts, want 1", code, got)
		}
	}
}

func TestRetriesServerErrorsAndRateLimits(t *testing.T) {
	for code, want := range map[int]int64{500: 2, 503: 2, 429: 3} {
		srv, n := countingServer(t, code, map[string]string{"Retry-After": "0"})
		c := NewClient("k", "m")
		c.BaseURL = srv.URL + "/"
		c.callWithRetry(context.Background(), "s", "u")
		if got := n.Load(); got != want {
			t.Errorf("%d: %d attempts, want %d", code, got, want)
		}
	}
}

// Retry-After from the server is honoured, within the caller's deadline.
func TestRetryAfterIsHonoured(t *testing.T) {
	srv, _ := countingServer(t, 429, map[string]string{"Retry-After": "1"})
	c := NewClient("k", "m")
	c.BaseURL = srv.URL + "/"
	start := time.Now()
	c.callWithRetry(context.Background(), "s", "u")
	if took := time.Since(start); took < 900*time.Millisecond {
		t.Errorf("returned after %v, want at least the 1s Retry-After", took)
	}
}

// A process-wide cap on in-flight Gemini calls: each request fans out to six
// calls, so without one a traffic burst multiplies straight into the quota.
func TestConcurrencyIsCapped(t *testing.T) {
	var inFlight, peak atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		inFlight.Add(-1)
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"[]"}]}}]}`))
	}))
	defer srv.Close()

	c := NewClient("k", "m")
	c.BaseURL = srv.URL + "/"
	c.SetMaxConcurrent(2)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() { c.Complete(context.Background(), "s", "u") })
	}
	wg.Wait()
	if p := peak.Load(); p > 2 {
		t.Errorf("peak concurrency %d, want at most 2", p)
	}
}
