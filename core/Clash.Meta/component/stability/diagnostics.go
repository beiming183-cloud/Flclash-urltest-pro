//go:build go1.21

package stability

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const LogLimit = 1 << 20
const ExportLimit = 2 << 20

type Event struct {
	Schema          int       `json:"schemaVersion"`
	Time            time.Time `json:"wallClock"`
	MonotonicMs     int64     `json:"monotonicMs"`
	Event           string    `json:"event"`
	Reason          string    `json:"reason,omitempty"`
	Group           string    `json:"groupId"`
	Node            string    `json:"nodeId,omitempty"`
	Epoch           uint64    `json:"networkEpoch"`
	Generation      uint64    `json:"routeGeneration"`
	LatencyMs       int64     `json:"latencyMs,omitempty"`
	FirstResponseMs int64     `json:"firstResponseMs,omitempty"`
	DialMs          int64     `json:"dialMs,omitempty"`
	HandshakeMs     int64     `json:"tlsHandshakeMs,omitempty"`
	OK              bool      `json:"ok"`
	Repeated        int       `json:"repeated,omitempty"`
	Phase           string    `json:"phase,omitempty"`
	Service         string    `json:"serviceTag,omitempty"`
}

type Journal struct {
	mu           sync.Mutex
	dir          string
	key          []byte
	start        time.Time
	now          func() time.Time
	debugUntil   time.Time
	dropped      int
	last         *Event
	lastSnapshot time.Time
	states       map[string]Snapshot
	tls          map[string][]TLSInfo
	business     *businessRecorder
}

var reasons = map[string]bool{"": true, "promote": true, "manual": true, "dial_failover": true, "network_changed": true, "network_offline": true, "relay_dial_timeout": true, "connect_rejected": true, "residential_auth": true, "tls_aborted": true, "cert_untrusted": true, "cert_hostname": true, "cert_expired": true, "unexpected_exit": true, "service_failed": true, "cancelled": true, "budget_exhausted": true, "unknown": true, "stopped": true}
var events = map[string]bool{"probe": true, "switch": true, "cooldown": true, "promote": true, "network_changed": true, "tls_check": true, "budget_exhausted": true, "user_mark": true, "snapshot": true}

func init() {
	events["startup_probe"] = true
	reasons["startup_revalidation"] = true
	for _, reason := range []string{"https_timeout", "tls_handshake_timeout", "exit_query_timeout", "latency_improved", "latency_quality_improved"} {
		reasons[reason] = true
	}
}

func OpenJournal(dir string, now func() time.Time) (*Journal, error) {
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, "identity.key")
	key, err := os.ReadFile(keyPath)
	if os.IsNotExist(err) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		err = os.WriteFile(keyPath, key, 0600)
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("diagnostic identity unavailable")
	}
	j := &Journal{dir: dir, key: key, start: now(), now: now, states: map[string]Snapshot{}, tls: map[string][]TLSInfo{}}
	j.business = &businessRecorder{j: j, active: map[string]*BusinessFlow{}, session: j.ID(time.Now().String())}
	if info, err := os.Stat(filepath.Join(dir, "statistics.json")); err == nil && info.Size() <= ExportLimit {
		data, err := os.ReadFile(filepath.Join(dir, "statistics.json"))
		if err == nil {
			var states map[string]Snapshot
			if json.Unmarshal(data, &states) == nil {
				j.states = states
			}
		}
	}
	return j, nil
}

func (j *Journal) TLS(node string, info []TLSInfo) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !validID(node) || len(info) > 3 {
		return
	}
	for _, item := range info {
		if item.Service != "control" && item.Service != "chatgpt" && item.Service != "android" {
			return
		}
		for _, hash := range []string{item.Certificate, item.SPKI} {
			if hash != "" {
				if len(hash) != 64 {
					return
				}
				if _, err := hex.DecodeString(hash); err != nil {
					return
				}
			}
		}
	}
	if len(j.tls) >= MaxCandidates && j.tls[node] == nil {
		for key := range j.tls {
			delete(j.tls, key)
			break
		}
	}
	j.tls[node] = append([]TLSInfo(nil), info...)
}

func (j *Journal) Statistics(group string) Snapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.states[group]
}

func (j *Journal) Store(group string, state Snapshot) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !validID(group) {
		return errors.New("invalid statistics identity")
	}
	if len(state.Selections) > 128 {
		return errors.New("selection history budget exceeded")
	}
	if len(state.StartupHints) > MaxCandidates {
		return errors.New("startup hint budget exceeded")
	}
	for _, hint := range state.StartupHints {
		if !validID(hint.Node) || hint.Score <= 0 || math.IsInf(hint.Score, 0) || math.IsNaN(hint.Score) || !reasons[hint.Error] {
			return errors.New("invalid startup hint")
		}
	}
	for _, event := range state.Selections {
		if !validSelection(event) {
			return errors.New("invalid selection history fields")
		}
	}
	checkNodes := func(nodes []Stats) bool {
		if len(nodes) > MaxCandidates {
			return false
		}
		for _, node := range nodes {
			if !validID(node.ID) || !validID(node.Relay) || !validID(node.Airport) || !reasons[node.LastError] {
				return false
			}
		}
		return true
	}
	if !checkNodes(state.Nodes) || !reasons[state.Reason] || len(state.Networks) > 8 {
		j.dropped++
		return errors.New("invalid statistics fields")
	}
	for key, nodes := range state.Networks {
		if !validID(key) || !checkNodes(nodes) {
			j.dropped++
			return errors.New("invalid network statistics fields")
		}
	}
	state.Primary = ""
	state.Active = nil
	state.Running = false
	previous, exists := j.states[group]
	j.states[group] = state
	data, err := json.Marshal(j.states)
	if err != nil || len(data) > ExportLimit {
		if exists {
			j.states[group] = previous
		} else {
			delete(j.states, group)
		}
		return errors.New("statistics budget exceeded")
	}
	tmp := filepath.Join(j.dir, "statistics.tmp")
	if err = os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(j.dir, "statistics.json"))
}

func validSelection(event SelectionEvent) bool {
	kind := event.Kind == "primary" || event.Kind == "backup" || event.Kind == "removed" || event.Kind == "rejected"
	reason := reasons[event.Reason] || event.Reason == "pool_selected" || event.Reason == "pool_replaced"
	return kind && reason && validID(event.Node) && (event.Previous == "" || validID(event.Previous))
}

func (j *Journal) ID(value string) string {
	mac := hmac.New(sha256.New, j.key)
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil)[:12])
}

func validID(id string) bool {
	if len(id) != 24 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func (j *Journal) Debug(enable bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.debugUntil = time.Time{}
	if enable {
		j.debugUntil = j.now().Add(600 * time.Second)
	}
}
func (j *Journal) DebugRemaining() int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return max(0, int64(j.debugUntil.Sub(j.now()).Seconds()))
}

func (j *Journal) write(event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > 4096 {
		j.dropped++
		return nil
	}
	path := filepath.Join(j.dir, "events.0.jsonl")
	info, _ := os.Stat(path)
	if info != nil && info.Size()+int64(len(data)) > LogLimit {
		for i := 3; i > 0; i-- {
			dst := filepath.Join(j.dir, "events."+string(rune('0'+i))+".jsonl")
			src := filepath.Join(j.dir, "events."+string(rune('0'+i-1))+".jsonl")
			if err = os.Remove(dst); err != nil && !os.IsNotExist(err) {
				return err
			}
			if err = os.Rename(src, dst); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func (j *Journal) Record(event Event) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	validPhase := event.Phase == "" || event.Phase == "delay" || event.Phase == "residential" || event.Phase == "tls" || event.Phase == "exit"
	validService := event.Service == "" || event.Service == "control" || event.Service == "control_backup" || event.Service == "chatgpt" || event.Service == "android" || event.Service == "exit"
	if !validPhase || !validService {
		j.dropped++
		return nil
	}
	if !events[event.Event] || !reasons[event.Reason] || !validID(event.Group) || (event.Node != "" && !validID(event.Node)) {
		j.dropped++
		return nil
	}
	now := j.now()
	event.Schema = 1
	event.Time = now.UTC()
	event.MonotonicMs = now.Sub(j.start).Milliseconds()
	if event.OK && event.Event == "probe" && !now.Before(j.debugUntil) {
		if now.Sub(j.lastSnapshot) < time.Minute {
			return nil
		}
		event.Event = "snapshot"
		j.lastSnapshot = now
	}
	if j.last != nil && !event.OK && event.Event == j.last.Event && event.Reason == j.last.Reason && event.Node == j.last.Node && event.Epoch == j.last.Epoch && now.Sub(j.last.Time) < 3*time.Second {
		j.last.Repeated++
		return nil
	}
	if j.last != nil {
		if err := j.write(*j.last); err != nil {
			return err
		}
	}
	j.last = &event
	return nil
}

func (j *Journal) Export(live ...map[string]Snapshot) ([]byte, error) {
	business, active, businessDropped, err := j.business.export()
	if err != nil {
		return nil, err
	}
	businessTruncated := 0
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.last != nil {
		if err := j.write(*j.last); err != nil {
			return nil, err
		}
		j.last = nil
	}
	lines := [][]byte{}
	cutoff := j.now().Add(-15 * time.Minute)
	for i := 3; i >= 0; i-- {
		f, err := os.Open(filepath.Join(j.dir, "events."+string(rune('0'+i))+".jsonl"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 4096), 8192)
		for scanner.Scan() {
			var event Event
			if json.Unmarshal(scanner.Bytes(), &event) == nil && !event.Time.Before(cutoff) {
				lines = append(lines, append([]byte(nil), scanner.Bytes()...))
			}
		}
		err = scanner.Err()
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	truncated := 0
	states := map[string]Snapshot{}
	for key, value := range j.states {
		states[key] = value
	}
	if len(live) > 0 {
		for key, value := range live[0] {
			if validID(key) && len(value.Nodes) <= MaxCandidates {
				states[key] = value
			}
		}
	}
	for {
		var buf bytes.Buffer
		z := zip.NewWriter(&buf)
		counts := map[string]int{}
		for _, line := range lines {
			var e Event
			json.Unmarshal(line, &e)
			counts[e.Reason] += 1 + e.Repeated
		}
		manifest := map[string]any{"schemaVersion": 2, "events": len(lines), "truncated": truncated, "dropped": j.dropped, "generatedAt": j.now().UTC(), "debugRemainingSec": max(0, int64(j.debugUntil.Sub(j.now()).Seconds())), "businessDiagnosticVersion": 1, "businessEvents": len(business), "businessActive": len(active), "businessDropped": businessDropped, "businessTruncated": businessTruncated, "businessScope": "passive-tcp", "businessRetentionMinutes": 30}
		if len(business) > 0 {
			manifest["businessCoverageStart"] = business[0].Time
			manifest["businessCoverageEnd"] = business[len(business)-1].Time
		}
		if len(lines) > 0 {
			var first, last Event
			json.Unmarshal(lines[0], &first)
			json.Unmarshal(lines[len(lines)-1], &last)
			manifest["coverageStart"] = first.Time
			manifest["coverageEnd"] = last.Time
		}
		for _, entry := range []struct {
			name string
			data any
		}{{"manifest.json", manifest}, {"summary.json", counts}, {"environment.json", map[string]any{"scope": "native-core", "containsProfiles": false, "appBaseline": "0.8.96", "diagnosticBuild": "2026101601", "businessObservation": "passive-tcp-no-payload"}}, {"states.json", states}, {"tls.json", map[string]any{"verification": "system-trust", "rawCertificates": false, "nodes": j.tls}}, {"chatgpt-active.json", active}} {
			w, err := z.Create(entry.name)
			if err != nil {
				return nil, err
			}
			if err = json.NewEncoder(w).Encode(entry.data); err != nil {
				return nil, err
			}
		}
		w, err := z.Create("events.jsonl")
		if err != nil {
			return nil, err
		}
		for _, line := range lines {
			w.Write(line)
			w.Write([]byte{'\n'})
		}
		w, err = z.Create("README.txt")
		if err != nil {
			return nil, err
		}
		io.WriteString(w, "Sanitized native stability diagnostics. No profiles, credentials or application payloads. Coverage and truncation are recorded in manifest.json.\nRead ChatGPT连接时间线.txt first; chatgpt-connections.jsonl contains passive TCP lifecycle records and chatgpt-active.json includes connections still open at export. No application SSL exceptions or decrypted TLS traffic are available.\n")
		w, err = z.Create("chatgpt-connections.jsonl")
		if err != nil {
			return nil, err
		}
		for _, event := range business {
			if err = json.NewEncoder(w).Encode(event); err != nil {
				return nil, err
			}
		}
		w, err = z.Create("ChatGPT连接时间线.txt")
		if err != nil {
			return nil, err
		}
		if _, err = io.WriteString(w, businessTimeline(business, active, businessDropped, businessTruncated, j.now())); err != nil {
			return nil, err
		}
		w, err = z.Create("nodes.csv")
		if err != nil {
			return nil, err
		}
		csvWriter := csv.NewWriter(w)
		csvWriter.Write([]string{"groupId", "nodeId", "sampleCount", "ewmaMs", "p95Ms", "reliabilityLower", "lastSuccess", "phase", "serviceTag", "error", "successStreak"})
		for group, state := range states {
			for _, node := range state.Nodes {
				csvWriter.Write([]string{group, node.ID, strconv.Itoa(node.Count), strconv.FormatFloat(node.EWMA, 'f', 2, 64), strconv.FormatFloat(node.P95, 'f', 2, 64), strconv.FormatFloat(node.Lower, 'f', 4, 64), node.LastSuccess.UTC().Format(time.RFC3339), node.LastPhase, node.LastService, node.LastError, strconv.Itoa(node.Streak)})
			}
		}
		csvWriter.Flush()
		if err = csvWriter.Error(); err != nil {
			return nil, err
		}
		if err = z.Close(); err != nil {
			return nil, err
		}
		if buf.Len() <= ExportLimit {
			return buf.Bytes(), nil
		}
		if len(lines) == 0 && len(business) == 0 {
			return nil, errors.New("summary exceeds export budget")
		}
		if len(business) >= len(lines) && len(business) > 0 {
			n := max(1, len(business)/10)
			business = business[n:]
			businessTruncated += n
			continue
		}
		n := max(1, len(lines)/10)
		lines = lines[n:]
		truncated += n
	}
}
