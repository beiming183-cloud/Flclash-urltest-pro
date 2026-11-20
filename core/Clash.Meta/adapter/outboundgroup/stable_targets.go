//go:build go1.21

package outboundgroup

import (
	"github.com/metacubex/mihomo/component/stability"
	"strings"
	"time"
)

func targetService(target string) string {
	host := strings.ToLower(target)
	for _, v := range []string{"chatgpt.com:", "openai.com:"} {
		if strings.Contains(host, v) {
			return "chatgpt"
		}
	}
	for _, v := range []string{"google.com:", "googleapis.com:", "googleusercontent.com:", "gstatic.com:"} {
		if strings.Contains(host, v) {
			return "google"
		}
	}
	if strings.Contains(host, "github.com:") || strings.Contains(host, "githubusercontent.com:") {
		return "github"
	}
	return "other"
}

// Bounded, in-memory target-specific circuit state. No URLs, query strings,
// credentials or payloads are stored; the key is an HMAC of host:port + node.
type targetFailure struct {
	count          int
	updated, until time.Time
}

func (s *Stable) targetKey(target, id string) string { return s.journal.ID(target + "\x00" + id) }

func (s *Stable) targetCooling(target, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().Before(s.targetFailures[s.targetKey(target, id)].until)
}

func (s *Stable) targetResult(target, id string, epoch uint64, ok bool, reason string, hard bool, elapsed time.Duration) {
	if s.engine.Snapshot().Epoch != epoch || reason == "cancelled" {
		return
	}
	key := s.targetKey(target, id)
	now := time.Now()
	s.mu.Lock()
	if s.targetFailures == nil {
		s.targetFailures = map[string]targetFailure{}
	}
	if ok {
		delete(s.targetFailures, key)
		s.mu.Unlock()
		return
	}
	for k, v := range s.targetFailures {
		if now.Sub(v.updated) > time.Minute {
			delete(s.targetFailures, k)
		}
	}
	if len(s.targetFailures) >= 128 {
		var oldest string
		var at time.Time
		for k, v := range s.targetFailures {
			if oldest == "" || v.updated.Before(at) {
				oldest, at = k, v.updated
			}
		}
		delete(s.targetFailures, oldest)
	}
	f := s.targetFailures[key]
	f.count++
	f.updated = now
	f.until = now.Add(time.Duration(min(f.count, 3)) * 5 * time.Second)
	s.targetFailures[key] = f
	if s.recheck == nil {
		s.recheck = map[string]time.Time{}
	}
	if s.recheckLast == nil {
		s.recheckLast = map[string]time.Time{}
	}
	if now.Sub(s.recheckLast[id]) >= 10*time.Second {
		s.recheck[id] = now
		s.recheckLast[id] = now
	}
	s.mu.Unlock()
	if hard || reason == "residential_auth" {
		s.engine.Record(id, stability.Sample{Epoch: epoch, Error: reason, Hard: hard, Phase: "residential", FailureWait: elapsed}, true)
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
