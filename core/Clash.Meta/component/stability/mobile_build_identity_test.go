package stability

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"testing"

	C "github.com/metacubex/mihomo/constant"
)

func TestDiagnosticExportsRunningCoreVersion(t *testing.T) {
	previous := C.Version
	C.Version = "mobile-audit-test-build"
	t.Cleanup(func() { C.Version = previous })
	j, err := OpenJournal(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := j.Export()
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range z.File {
		if f.Name != "environment.json" {
			continue
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		var env map[string]any
		if err := json.NewDecoder(r).Decode(&env); err != nil {
			t.Fatal(err)
		}
		if env["coreVersion"] != C.Version || env["diagnosticBuild"] != C.Version {
			t.Fatalf("export misidentifies running core: %v", env)
		}
		return
	}
	t.Fatal("environment.json missing")
}
