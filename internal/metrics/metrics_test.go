package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// scrape fetches /metrics from the given server and returns the body.
func scrape(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("scrape /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /metrics: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", resp.StatusCode)
	}
	return string(body)
}

func TestMetricsMiddlewareRecordsRequest(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("pong"))
	})
	mux.Handle("GET /metrics", Handler())
	srv := httptest.NewServer(MetricsMiddleware(mux))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ping")
	if err != nil {
		t.Fatalf("get /ping: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("ping status = %d, want 418", resp.StatusCode)
	}

	body := scrape(t, srv)

	// Prometheus emits labels in alphabetical order (code before method).
	for _, want := range []string{
		`tuitio_http_requests_total{code="418",method="GET"}`,
		`tuitio_http_request_duration_seconds_count{code="418",method="GET"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q\n---\n%s", want, body)
		}
	}
}

func TestMetricsMiddlewareCountsInFlight(t *testing.T) {
	block := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
		<-block
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("GET /metrics", Handler())
	srv := httptest.NewServer(MetricsMiddleware(mux))
	defer srv.Close()

	done := make(chan struct{})
	go func() {
		resp, err := http.Get(srv.URL + "/slow")
		if err == nil {
			resp.Body.Close()
		}
		close(done)
	}()

	// The /slow request stays in flight; /metrics is excluded from the
	// middleware, so every scrape reads exactly 1 while it is pending.
	deadline := time.After(3 * time.Second)
	for {
		if strings.Contains(scrape(t, srv), "tuitio_http_requests_in_flight 1") {
			break
		}
		select {
		case <-deadline:
			close(block) // release /slow so Close cannot hang
			t.Fatalf("in-flight gauge never reached 1")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	close(block) // release /slow so the goroutine can finish
	<-done
}

func TestRecordIndexerProgressSetsLag(t *testing.T) {
	// Lag is clamped at zero when caught up or ahead of the tip.
	RecordIndexerProgress(100, 100)
	body := scrape(t, mustServer(t))
	if !strings.Contains(body, "tuitio_indexer_lag_ledgers 0") {
		t.Errorf("expected lag 0 after catch-up, got:\n%s", body)
	}

	RecordIndexerProgress(100, 150) // 50 ledgers behind
	body = scrape(t, mustServer(t))
	if !strings.Contains(body, "tuitio_indexer_lag_ledgers 50") {
		t.Errorf("expected lag 50, got:\n%s", body)
	}

	// A stale cursor ahead of the tip must not produce a negative gauge.
	RecordIndexerProgress(200, 150)
	body = scrape(t, mustServer(t))
	if !strings.Contains(body, "tuitio_indexer_lag_ledgers 0") {
		t.Errorf("expected lag clamped to 0, got:\n%s", body)
	}
}

// mustServer builds a standalone /metrics server for direct scrape tests.
func mustServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", Handler())
	return httptest.NewServer(mux)
}
