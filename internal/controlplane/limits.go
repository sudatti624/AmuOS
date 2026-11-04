package controlplane

import (
	"net"
	"net/http"
	"time"
)

const (
	authAttempts       = 5
	authWindow         = 15 * time.Minute
	authClients        = 65536
	authOverflowKey    = "overflow"
	authOverflowLimit  = 64
	rejectClientLimit  = 60
	rejectGlobalLimit  = 600
	rejectWindow       = time.Minute
	rejectClients      = 4096
	rejectOverflowKey  = "overflow"
	rejectSummaryRoute = "http"
)

// clientKey groups a remote address for rate limiting. An IPv6 client usually
// controls a whole /64, so the prefix counts as one client.
func clientKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// prune drops expired buckets once the table is full, so a normal request
// does not scan the whole table.
func prune(table map[string]bucket, limit int, now time.Time) {
	if len(table) < limit {
		return
	}
	for k, v := range table {
		if !now.Before(v.Until) {
			delete(table, k)
		}
	}
}

func (s *Server) authAllowed(w http.ResponseWriter, r *http.Request) bool {
	now := s.clock()
	key, limit := clientKey(r.RemoteAddr), authAttempts
	s.mu.Lock()
	prune(s.attempts, authClients, now)
	b, exists := s.attempts[key]
	if exists && !now.Before(b.Until) {
		b, exists = bucket{}, false
	}
	if !exists && len(s.attempts) >= authClients {
		// A full table must not lock out every new client: they share one
		// larger bucket, so filling the table needs very many addresses and
		// still leaves room for legitimate sign-ins.
		key, limit = authOverflowKey, authOverflowLimit
		b, exists = s.attempts[key]
		if exists && !now.Before(b.Until) {
			b, exists = bucket{}, false
		}
	}
	if b.Count >= limit {
		s.mu.Unlock()
		w.Header().Set("Retry-After", "900")
		s.reject(w, r, 429, "rate_limited")
		return false
	}
	if !exists {
		b.Until = now.Add(authWindow)
	}
	b.Count++
	s.attempts[key] = b
	s.mu.Unlock()
	return true
}

// rejectionLimiter bounds how many rejected requests are written to the audit
// log. Rejections over the limit are still answered normally and are counted
// into one summary event, so unauthenticated traffic cannot grow the audit log
// or hold the single database connection without bound.
type rejectionLimiter struct {
	clients    map[string]bucket
	global     bucket
	suppressed int
}

// admitRejection reports whether this rejection is recorded individually and
// how many earlier rejections from a finished window need a summary event.
func (s *Server) admitRejection(r *http.Request) (record bool, summary int) {
	now := s.clock()
	l := &s.rejections
	s.mu.Lock()
	defer s.mu.Unlock()
	if !now.Before(l.global.Until) {
		summary, l.suppressed = l.suppressed, 0
		l.global = bucket{Until: now.Add(rejectWindow)}
	}
	prune(l.clients, rejectClients, now)
	key := clientKey(r.RemoteAddr)
	b, exists := l.clients[key]
	if exists && !now.Before(b.Until) {
		b, exists = bucket{}, false
	}
	if !exists && len(l.clients) >= rejectClients {
		key = rejectOverflowKey
		b, exists = l.clients[key]
		if exists && !now.Before(b.Until) {
			b, exists = bucket{}, false
		}
	}
	if !exists {
		b.Until = now.Add(rejectWindow)
	}
	if b.Count >= rejectClientLimit || l.global.Count >= rejectGlobalLimit {
		l.suppressed++
		return false, summary
	}
	b.Count++
	l.clients[key] = b
	l.global.Count++
	return true, summary
}
