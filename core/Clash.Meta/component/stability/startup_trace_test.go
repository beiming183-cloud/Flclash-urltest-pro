package stability

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestStartupTraceSurvivesDetailedEventExpiry(t *testing.T) {
	now := time.Now()
	j, err := OpenJournal(t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	group := j.ID("group")
	for _, kind := range []string{"startup_begin", "startup_candidates", "startup_probe", "startup_probe_result", "primary_changed", "dial_final", "dial_final"} {
		now = now.Add(time.Second)
		if err := j.Record(Event{Group: group, Event: kind, OK: true}); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(time.Hour)
	data, err := j.Export()
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	f, err := archive.Open("startup.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var traces map[string]startupTrace
	if err := json.NewDecoder(f).Decode(&traces); err != nil {
		t.Fatal(err)
	}
	trace := traces[group]
	if trace.FirstDial == nil || trace.FirstDial.MonotonicMs != 6000 || trace.FirstProbe == nil || trace.FirstPrimary == nil || trace.Candidates == nil || len(trace.Probes) != 1 {
		t.Fatalf("incomplete trace: %+v", trace)
	}
}
