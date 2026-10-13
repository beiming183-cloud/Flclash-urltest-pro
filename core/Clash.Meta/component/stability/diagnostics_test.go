package stability

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticBoundsSanitizationAndDebugExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	dir := t.TempDir()
	j, err := OpenJournal(dir, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	j.Debug(true)
	for i := 0; i < 100000; i++ {
		now = now.Add(time.Millisecond)
		if err = j.Record(Event{Event: "probe", Reason: "tls_aborted", Group: j.ID("private-group"), Node: j.ID(string(rune(i))), Epoch: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err = j.Record(Event{Event: "probe", Reason: "password=secret https://private.invalid/token", Group: j.ID("private-group")}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(601 * time.Second)
	if j.DebugRemaining() != 0 {
		t.Fatal("debug did not expire")
	}
	data, err := j.Export()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > ExportLimit {
		t.Fatal("oversized ZIP")
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.File {
		rc, _ := f.Open()
		body, _ := io.ReadAll(rc)
		rc.Close()
		for _, secret := range []string{"password", "secret", "private.invalid", "private-group"} {
			if strings.Contains(string(body), secret) {
				t.Fatal("secret exported", f.Name)
			}
		}
		if strings.HasSuffix(f.Name, ".json") && !json.Valid(body) {
			t.Fatal("invalid JSON")
		}
	}
	var total int64
	files, _ := filepath.Glob(filepath.Join(dir, "events.*.jsonl"))
	for _, f := range files {
		info, _ := os.Stat(f)
		if info.Size() > LogLimit {
			t.Fatal("oversized log")
		}
		total += info.Size()
	}
	if total > 4*LogLimit {
		t.Fatal("rotation limit exceeded")
	}
	j2, err := OpenJournal(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if j2.ID("same") != j.ID("same") {
		t.Fatal("identity changed across restart")
	}
	if j2.DebugRemaining() != 0 {
		t.Fatal("debug persisted across restart")
	}
}
