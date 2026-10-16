package stability

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func businessArchive(t *testing.T, j *Journal) map[string][]byte {
	t.Helper()
	data, err := j.Export()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > ExportLimit {
		t.Fatal("export exceeds budget")
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range z.File {
		r, _ := f.Open()
		files[f.Name], err = io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

func TestBusinessTransportAnomalyExcludesThinkingEOFAndCancellation(t *testing.T) {
	now := time.Unix(1000, 0)
	j, _ := OpenJournal(t.TempDir(), func() time.Time { return now })
	var observed []string
	RegisterBusinessAnomalyObserver(func(route BusinessRoute, reason string) {
		j.business.mu.Lock()
		j.business.mu.Unlock()
		observed = append(observed, reason)
	})
	defer RegisterBusinessAnomalyObserver(nil)
	f := j.business.begin("android.chat.openai.com", "com.openai.chatgpt")
	f.DialAttempt(BusinessRoute{Group: j.ID("group"), Node: j.ID("node"), Epoch: 1}, "IP1")
	f.Open([]string{"IP1"}, 100)
	now = now.Add(20 * time.Second)
	j.BusinessTick("", Snapshot{})
	f.CopyEnded("download", io.EOF)
	f.CopyEnded("download", context.Canceled)
	f.CopyEnded("download", nil)
	if len(observed) != 0 {
		t.Fatal("thinking, EOF or cancellation triggered a route fault")
	}
	f.CopyEnded("download", syscall.ECONNRESET)
	if len(observed) != 1 || observed[0] != "connection_reset" {
		t.Fatal("genuine transport failure did not trigger recheck")
	}
	f.Finish()
}

func TestBusinessObservesBlackholeRecoveryAndOldConnection(t *testing.T) {
	now := time.Unix(1000, 0)
	j, err := OpenJournal(t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	f := j.business.begin("android.chat.openai.com", "com.openai.chatgpt")
	id := j.ID("node-original")
	route := BusinessRoute{Group: j.ID("group:IP1"), Node: id, Protocol: "Socks5", Epoch: 1, Generation: 1}
	f.DialAttempt(route, "IP1-一分机场-013-S5")
	f.DialResult(nil, 250*time.Millisecond)
	f.Open([]string{"IP1-一分机场-013-S5", "IP1", "出口"}, 517)
	now = now.Add(9 * time.Second)
	j.BusinessTick(route.Group, Snapshot{Primary: id, Epoch: 1, Generation: 1})
	j.BusinessTick(route.Group, Snapshot{Primary: id, Epoch: 1, Generation: 2})
	newID := j.ID("node-new")
	j.BusinessTick(route.Group, Snapshot{Primary: newID, Epoch: 2, Generation: 2})
	j.MarkBusiness(route.Group)
	f.Bytes(false, 101)
	f.CopyEnded("download", syscall.ECONNRESET)
	f.Finish()
	files := businessArchive(t, j)
	text := string(files["ChatGPT连接时间线.txt"])
	for _, want := range []string{"上传后暂未收到下行（仅观察）", "换线/网络变化后旧连接仍在", "停滞后再次收到下行", "connection_reset", "IP1-一分机场-013-S5", "上传 517B / 下行 101B", "用户标记故障时连接"} {
		if !strings.Contains(text, want) {
			t.Fatal("missing", want, text)
		}
	}
	lines := bytes.Split(bytes.TrimSpace(files["chatgpt-connections.jsonl"]), []byte{'\n'})
	found := 0
	for _, line := range lines {
		var e BusinessEvent
		if json.Unmarshal(line, &e) != nil || !validBusinessEvent(e) {
			t.Fatal("invalid flow event")
		}
		if e.Event == "old_connection_after_route_change" {
			found++
			if e.Route.Node != id || e.CurrentNode != newID || e.Route.Epoch != 1 || e.CurrentEpoch != 2 {
				t.Fatal("rewrote bound route", e)
			}
		}
	}
	if found != 1 || len(j.business.active) != 0 {
		t.Fatal("route change or cleanup missing")
	}
}

func TestBusinessIdleIsNotSSLFailureAndExportIncludesActive(t *testing.T) {
	now := time.Unix(1000, 0)
	j, _ := OpenJournal(t.TempDir(), func() time.Time { return now })
	f := j.business.begin("ws.chatgpt.com", "")
	f.Open([]string{"DIRECT"}, 0)
	now = now.Add(time.Minute)
	j.BusinessTick("", Snapshot{})
	files := businessArchive(t, j)
	if bytes.Contains(files["chatgpt-connections.jsonl"], []byte("no_downstream_observed")) {
		t.Fatal("idle misclassified as waiting for a reply")
	}
	var active []BusinessEvent
	if json.Unmarshal(files["chatgpt-active.json"], &active) != nil || len(active) != 1 || active[0].Chain[0] != "DIRECT" {
		t.Fatal("missing live direct connection")
	}
	f.Finish()
}

func TestBusinessPrivacyBoundsAndRestart(t *testing.T) {
	j, _ := OpenJournal(t.TempDir(), nil)
	if j.business.begin("notopenai.com", "") != nil || j.business.begin("chatgpt.com.attacker.invalid", "") != nil {
		t.Fatal("unrelated domain recorded")
	}
	f := j.business.begin("private-token.oaiusercontent.com", "com.openai.chatgpt")
	f.DialAttempt(BusinessRoute{Group: j.ID("group"), Node: j.ID("node"), Protocol: "password=secret"}, "https://user:secret@private.invalid/token")
	f.DialResult(errors.New("password=secret bearer-token"), time.Second)
	f.Open([]string{"https://secret.invalid", "IP1"}, 0)
	f.Failed("connection_dial_failed", errors.New("Bearer super-secret"))
	f.Finish()
	flows := []*BusinessFlow{}
	for i := 0; i < businessFlowLimit+10; i++ {
		if x := j.business.begin("chatgpt.com", ""); x != nil {
			flows = append(flows, x)
		}
	}
	if len(j.business.active) != businessFlowLimit || j.business.dropped != 10 {
		t.Fatal("active flow budget failed")
	}
	for i := 0; i < businessQueueLimit+10; i++ {
		flows[0].Failed("retry_failed", context.DeadlineExceeded)
	}
	flows[0].DialResult(nil, time.Second)
	if len(j.business.pending) > businessQueueLimit {
		t.Fatal("queue unbounded")
	}
	for _, x := range flows {
		x.Finish()
	}
	files := businessArchive(t, j)
	for name, body := range files {
		for _, secret := range []string{"private-token", "password=", "super-secret", "bearer-token", "private.invalid", "secret.invalid"} {
			if bytes.Contains(body, []byte(secret)) {
				t.Fatal("secret exported", name, secret)
			}
		}
	}
	j2, _ := OpenJournal(j.dir, nil)
	files = businessArchive(t, j2)
	if len(bytes.TrimSpace(files["chatgpt-connections.jsonl"])) == 0 {
		t.Fatal("logs not preserved across restart")
	}
	j2.business.begin("chatgpt.com", "").Finish()
	if j.business.session == j2.business.session {
		t.Fatal("sessions share flow identity")
	}
}

func TestBusinessConcurrentSamplingAndRotation(t *testing.T) {
	j, _ := OpenJournal(t.TempDir(), nil)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				f := j.business.begin("chatgpt.com", "")
				f.Open([]string{"DIRECT"}, 0)
				f.Bytes(true, 50)
				f.Bytes(false, 30)
				f.CopyEnded("upload", nil)
				f.Finish()
			}
		}()
	}
	for i := 0; i < 20; i++ {
		j.BusinessTick("", Snapshot{})
		if _, err := j.Export(); err != nil {
			t.Error(err)
		}
	}
	wg.Wait()
	for i := 0; i < 8; i++ {
		for n := 0; n < 1500; n++ {
			f := j.business.begin("chatgpt.com", "")
			f.Open([]string{"DIRECT"}, 0)
			f.Bytes(true, 1)
			f.Finish()
		}
		if err := j.business.flush(true); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(j.dir, "chatgpt.*.jsonl"))
	if len(files) > 4 {
		t.Fatal("rotation count exceeded")
	}
	for _, file := range files {
		info, _ := os.Stat(file)
		if info.Size() > businessLogLimit {
			t.Fatal("rotation size exceeded")
		}
	}
	businessArchive(t, j)
}
