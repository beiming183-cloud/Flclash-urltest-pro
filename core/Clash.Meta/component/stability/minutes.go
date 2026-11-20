//go:build go1.21

package stability

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const minuteFileLimit = 2 << 20

// Small long-term aggregates supplement the intentionally short detailed trace.
// Counters are taken before sampling/deduplication; dial timings are CONNECT
// establishment only, not HTTP completion, download speed or first-token time.
type MinuteSummary struct {
	Minute         time.Time      `json:"minute"`
	Group          string         `json:"groupId"`
	Session        string         `json:"session"`
	Counts         map[string]int `json:"counts,omitempty"`
	Observations   int            `json:"observations,omitempty"`
	ActiveMin      int            `json:"activeMin"`
	ActiveMax      int            `json:"activeMax"`
	BudgetMax      int            `json:"budgetMax,omitempty"`
	MaintenanceMax int            `json:"maintenanceHourMax,omitempty"`
	RecoveryMax    int            `json:"recoveryHourMax,omitempty"`
	ObservedMs     int64          `json:"observedMs,omitempty"`
	EmptyMs        int64          `json:"emptyMs,omitempty"`
	OneRouteMs     int64          `json:"oneRouteMs,omitempty"`
	TwoRoutesMs    int64          `json:"twoRoutesMs,omitempty"`
	UnderfilledMs  int64          `json:"underfilledMs,omitempty"`
	StoppedMs      int64          `json:"stoppedMs,omitempty"`
	DialSamples    int            `json:"dialSamples,omitempty"`
	DialP50Ms      int64          `json:"connectP50Ms,omitempty"`
	DialP95Ms      int64          `json:"connectP95Ms,omitempty"`
}
type minuteAccumulator struct {
	row                  MinuteSummary
	dial                 []int64
	last                 time.Time
	underfilled, stopped bool
	active               int
}

func validGuardCode(code string) bool {
	switch code {
	case "", "allowed", "status_unavailable", "status_invalid", "policy_not_applied", "campus_restricted", "heartbeat_stale", "publication_busy":
		return true
	}
	return false
}

func (j *Journal) minuteLocked(group string, now time.Time) *minuteAccumulator {
	if j.minutes == nil {
		j.minutes = map[string]*minuteAccumulator{}
	}
	m := j.minutes[group]
	minute := now.UTC().Truncate(time.Minute)
	if m != nil && !m.row.Minute.Equal(minute) {
		m.accrue(minute)
		if err := j.writeMinuteLocked(m.summary()); err != nil {
			j.dropped++
		}
		delete(j.minutes, group)
		m = nil
	}
	if m == nil {
		if len(j.minutes) >= 8 {
			return nil
		}
		m = &minuteAccumulator{row: MinuteSummary{Minute: minute, Group: group, Session: j.start.UTC().Format(time.RFC3339Nano), Counts: map[string]int{}}}
		j.minutes[group] = m
	}
	return m
}

func (m *minuteAccumulator) accrue(now time.Time) {
	if m.last.IsZero() {
		return
	}
	elapsed := now.Sub(m.last)
	// Do not invent observation time through suspension or stalled tickers.
	if elapsed < 0 || elapsed > 2*time.Second {
		return
	}
	ms := elapsed.Milliseconds()
	m.row.ObservedMs += ms
	if m.underfilled {
		m.row.UnderfilledMs += ms
	}
	switch m.active {
	case 0:
		m.row.EmptyMs += ms
	case 1:
		m.row.OneRouteMs += ms
	case 2:
		m.row.TwoRoutesMs += ms
	}
	if m.stopped {
		m.row.StoppedMs += ms
	}
	m.last = now
}

func (m *minuteAccumulator) summary() MinuteSummary {
	row := m.row
	row.DialSamples = len(m.dial)
	if len(m.dial) > 0 {
		values := append([]int64(nil), m.dial...)
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		row.DialP50Ms = values[(len(values)-1)*50/100]
		row.DialP95Ms = values[(len(values)-1)*95/100]
	}
	return row
}

func (j *Journal) countMinuteLocked(e Event) {
	m := j.minuteLocked(e.Group, e.Time)
	if m == nil {
		return
	}
	key := e.Event
	if e.Event == "probe" || e.Event == "dial_result" || e.Event == "dial_final" {
		if e.OK {
			key += ".ok"
		} else {
			key += ".failed"
		}
	}
	if e.Reason != "" && (e.Event == "dial_failed" || e.Event == "lifecycle") {
		key += "." + e.Reason
	}
	m.row.Counts[key]++
	if e.Event == "dial_final" {
		outcome := "ok"
		if !e.OK {
			outcome = "failed"
			if e.Reason == "cancelled" {
				outcome = "cancelled"
			}
		}
		m.row.Counts["target."+e.Service+"."+outcome]++
	}
	if e.Event == "lifecycle" && e.GuardCode != "" {
		m.row.Counts["guard."+e.GuardCode]++
	}
	if e.Event == "dial_result" && e.OK {
		if len(m.dial) < 512 {
			m.dial = append(m.dial, e.DialMs)
		} else {
			m.dial[(m.row.Counts["dial_result.ok"]-1)%512] = e.DialMs
		}
	}
}

func (j *Journal) ObserveMinute(group string, state Snapshot) {
	if !validID(group) {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.now()
	m := j.minuteLocked(group, now)
	if m == nil {
		return
	}
	m.accrue(now)
	m.last = now
	n := len(state.Active)
	if m.row.Observations == 0 || n < m.row.ActiveMin {
		m.row.ActiveMin = n
	}
	if n > m.row.ActiveMax {
		m.row.ActiveMax = n
	}
	m.row.Observations++
	m.row.BudgetMax = max(m.row.BudgetMax, state.BudgetUsed)
	m.row.MaintenanceMax = max(m.row.MaintenanceMax, state.ProbeBudgets[MaintenanceProbe].HourRequests)
	m.row.RecoveryMax = max(m.row.RecoveryMax, state.ProbeBudgets[RecoveryProbe].HourRequests+state.ProbeBudgets[SearchProbe].HourRequests)
	m.active = n
	m.underfilled = n < 3
	m.stopped = !state.Running
}

func (j *Journal) writeMinuteLocked(row MinuteSummary) error {
	path := filepath.Join(j.dir, "minutes.0.jsonl")
	data, err := json.Marshal(row)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if st, err := os.Stat(path); err == nil && st.Size()+int64(len(data)) > minuteFileLimit {
		older := filepath.Join(j.dir, "minutes.1.jsonl")
		if err = os.Remove(older); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err = os.Rename(path, older); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
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

func (j *Journal) exportMinutesLocked() ([]MinuteSummary, error) {
	rows := []MinuteSummary{}
	cutoff := j.now().Add(-48 * time.Hour)
	for i := 1; i >= 0; i-- {
		name := "minutes.0.jsonl"
		if i == 1 {
			name = "minutes.1.jsonl"
		}
		f, err := os.Open(filepath.Join(j.dir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 4096), 32768)
		for scanner.Scan() {
			var row MinuteSummary
			if json.Unmarshal(scanner.Bytes(), &row) == nil && validID(row.Group) && !row.Minute.Before(cutoff) {
				rows = append(rows, row)
			}
		}
		err = scanner.Err()
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	for _, m := range j.minutes {
		if !m.row.Minute.Before(cutoff) {
			rows = append(rows, m.summary())
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Minute.Before(rows[j].Minute) })
	return rows, nil
}
