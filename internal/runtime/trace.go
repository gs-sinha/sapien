package runtime

import (
	"crypto/tls"
	"net/http/httptrace"
	"sync"
	"time"

	"github.com/growsimplee/sapien/internal/domain"
)

// traceTimer accumulates httptrace callback timestamps for one request and
// converts them into domain.Timings. A single request may hop through
// several redirects; each phase is recorded from its most recent occurrence.
type traceTimer struct {
	mu sync.Mutex

	dnsStart, dnsDone         time.Time
	connectStart, connectDone time.Time
	tlsStart, tlsDone         time.Time
	firstByte                 time.Time
}

func (t *traceTimer) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) {
			t.mu.Lock()
			t.dnsStart = time.Now()
			t.mu.Unlock()
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			t.mu.Lock()
			t.dnsDone = time.Now()
			t.mu.Unlock()
		},
		ConnectStart: func(_, _ string) {
			t.mu.Lock()
			t.connectStart = time.Now()
			t.mu.Unlock()
		},
		ConnectDone: func(_, _ string, _ error) {
			t.mu.Lock()
			t.connectDone = time.Now()
			t.mu.Unlock()
		},
		TLSHandshakeStart: func() {
			t.mu.Lock()
			t.tlsStart = time.Now()
			t.mu.Unlock()
		},
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			t.mu.Lock()
			t.tlsDone = time.Now()
			t.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			t.mu.Lock()
			t.firstByte = time.Now()
			t.mu.Unlock()
		},
	}
}

func msSince(a, b time.Time) float64 {
	if a.IsZero() || b.IsZero() || b.Before(a) {
		return 0
	}
	return float64(b.Sub(a).Microseconds()) / 1000.0
}

func (t *traceTimer) timings(start time.Time, total time.Duration) domain.Timings {
	t.mu.Lock()
	defer t.mu.Unlock()
	var ttfb float64
	if !t.firstByte.IsZero() {
		ttfb = msSince(start, t.firstByte)
	}
	return domain.Timings{
		DNSMs:     msSince(t.dnsStart, t.dnsDone),
		ConnectMs: msSince(t.connectStart, t.connectDone),
		TLSMs:     msSince(t.tlsStart, t.tlsDone),
		TTFBMs:    ttfb,
		TotalMs:   float64(total.Microseconds()) / 1000.0,
	}
}
