package stability

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestExportIncludesLivePrimaryAndCurrentSamples(t *testing.T) {
	j, err := OpenJournal(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	group := j.ID("group")
	node := j.ID("node")
	state := Snapshot{Primary: node, Active: []string{node}, Running: true, Nodes: []Stats{{Candidate: Candidate{node, j.ID("relay"), j.ID("airport")}, Count: 7, EWMA: 300, LastSuccess: time.Now()}}}
	data, err := j.Export(map[string]Snapshot{group: state})
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, f := range z.File {
		if f.Name != "states.json" && f.Name != "nodes.csv" {
			continue
		}
		r, _ := f.Open()
		body, _ := io.ReadAll(r)
		r.Close()
		if !strings.Contains(string(body), node) {
			t.Fatal("live node missing")
		}
		if f.Name == "states.json" && !strings.Contains(string(body), `"primary":"`+node+`"`) {
			t.Fatal("primary omitted")
		}
		if f.Name == "nodes.csv" && !strings.Contains(string(body), ",7,300.00,") {
			t.Fatal("stale statistics exported")
		}
		found++
	}
	if found != 2 {
		t.Fatal("live diagnostic files missing")
	}
}
