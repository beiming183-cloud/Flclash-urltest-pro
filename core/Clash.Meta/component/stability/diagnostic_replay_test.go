package stability

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"
)

func TestDiagnosticReplaySelection(t *testing.T) {
	path := os.Getenv("STABILITY_DIAGNOSTIC_ZIP")
	if path == "" {
		t.Skip("Set STABILITY_DIAGNOSTIC_ZIP to replay a private diagnostic archive")
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	read := func(name string, target any) {
		t.Helper()
		for _, file := range archive.File {
			if file.Name != name {
				continue
			}
			stream, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(io.LimitReader(stream, 2<<20))
			stream.Close()
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, target); err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Fatalf("Missing %s", name)
	}
	var manifest struct {
		Start time.Time `json:"coverageStart"`
		End   time.Time `json:"coverageEnd"`
	}
	var groups map[string]Snapshot
	read("manifest.json", &manifest)
	read("states.json", &groups)
	for _, state := range groups {
		now := manifest.End
		candidates := make([]Candidate, 0, len(state.Nodes))
		for _, node := range state.Nodes {
			candidates = append(candidates, node.Candidate)
		}
		engine, err := New(candidates, func() time.Time { return now })
		if err != nil {
			t.Fatal(err)
		}
		engine.epoch = state.Epoch
		engine.primary = state.Primary
		engine.active = append([]string(nil), state.Active...)
		engine.lastSwitch = manifest.Start
		engine.powerSave = state.PowerSave
		engine.paused = state.Paused
		for _, node := range state.Nodes {
			copy := node
			copy.decayedAt = now
			copy.FirstSuccess = copy.LastSuccess.Add(-10 * time.Second)
			engine.nodes[node.ID] = &copy
		}
		engine.Attempts()
		primary := engine.nodes[state.Primary]
		selected := engine.nodes[engine.Snapshot().Primary]
		if selected == nil || LatencyCost(*selected) >= LatencyCost(*primary) {
			t.Fatal("Revised election did not improve the captured slow primary")
		}
		var challenger *Stats
		for _, id := range engine.order {
			node := engine.nodes[id]
			if id != state.Primary && engine.fresh(node, now) && node.Failure == 0 && node.Count >= 2 &&
				node.P95 < primary.P95*.7 && node.EWMA+150 < primary.EWMA &&
				(challenger == nil || node.EWMA < challenger.EWMA) {
				challenger = node
			}
		}
		if challenger == nil {
			t.Fatal("Archive does not contain the reported faster verified challenger")
		}
		t.Logf("CAPTURE: old primary %.2fms P95 %.0fms, %d samples, failures %.2f, lower %.4f; challenger %.2fms P95 %.0fms, %d samples, failures %.2f, lower %.4f",
			primary.EWMA, primary.P95, primary.Count, primary.Failure, primary.Lower,
			challenger.EWMA, challenger.P95, challenger.Count, challenger.Failure, challenger.Lower)
		latency := time.Duration(challenger.EWMA * float64(time.Millisecond))
		originalCount := challenger.Count
		for i := 0; i < 40 && engine.Snapshot().Primary != challenger.ID; i++ {
			now = now.Add(time.Second)
			engine.Record(challenger.ID, Sample{Epoch: state.Epoch, OK: true, TLS: true, Exit: true, Latency: latency}, true)
		}
		if engine.Snapshot().Primary != challenger.ID {
			t.Fatal("Validated low-latency challenger was not selected after the switch hold")
		}
		t.Logf("REVISED: low-latency challenger is selected after validation and switch hold, %d extra samples (%d total), reason=%s",
			challenger.Count-originalCount, challenger.Count, engine.Snapshot().Reason)
	}
}
