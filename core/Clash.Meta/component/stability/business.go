//go:build go1.21

package stability

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const businessLogLimit = 512 << 10
const businessFlowLimit = 256
const businessQueueLimit = 2048

type BusinessRoute struct {
	Group      string `json:"groupId,omitempty"`
	Node       string `json:"nodeId,omitempty"`
	Label      string `json:"label,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	Epoch      uint64 `json:"networkEpoch,omitempty"`
	Generation uint64 `json:"routeGeneration,omitempty"`
}

type BusinessEvent struct {
	Time              time.Time     `json:"wallClock"`
	ID                string        `json:"connectionId"`
	Event             string        `json:"event"`
	Target            string        `json:"target"`
	Source            string        `json:"source"`
	Route             BusinessRoute `json:"route"`
	RootProxy         string        `json:"selectedProxy,omitempty"`
	Chain             []string      `json:"chain,omitempty"`
	ElapsedMs         int64         `json:"elapsedMs"`
	LastUpload        time.Time     `json:"lastUploadAt,omitempty"`
	LastDownload      time.Time     `json:"lastDownloadAt,omitempty"`
	DialMs            int64         `json:"dialMs,omitempty"`
	Upload            int64         `json:"uploadBytes"`
	Download          int64         `json:"downloadBytes"`
	WaitingMs         int64         `json:"noDownloadAfterUploadMs,omitempty"`
	Direction         string        `json:"direction,omitempty"`
	Reason            string        `json:"reason,omitempty"`
	Attempt           int           `json:"attempt,omitempty"`
	CurrentNode       string        `json:"currentPrimaryId,omitempty"`
	CurrentEpoch      uint64        `json:"currentNetworkEpoch,omitempty"`
	CurrentGeneration uint64        `json:"currentRouteGeneration,omitempty"`
}

type BusinessFlow struct {
	owner          *businessRecorder
	event          BusinessEvent
	started        time.Time
	lastUpload     time.Time
	lastDownload   time.Time
	waitingSince   time.Time
	lastSnapshot   time.Time
	warned         bool
	closed         bool
	connected      bool
	firstUpload    bool
	firstDownload  bool
	seenEpoch      uint64
	seenGeneration uint64
}

type businessRecorder struct {
	mu        sync.Mutex
	flushMu   sync.Mutex
	j         *Journal
	active    map[string]*BusinessFlow
	pending   []BusinessEvent
	dropped   int
	sequence  uint64
	session   string
	lastFlush time.Time
}

var businessOwner atomic.Pointer[businessRecorder]

type businessContextKey struct{}

var safeNodeLabel = regexp.MustCompile(`^(IP[12]|RF2-Relay)-[\p{Han}A-Za-z]{1,24}-[0-9]{1,4}(-S5)?$`)
var anonymousNodeLabel = regexp.MustCompile(`^节点-[a-f0-9]{8}$`)

func (j *Journal) ObserveBusiness() {
	businessOwner.Store(j.business)
}

func businessTarget(host, process string) (string, string) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	app := process == "com.openai.chatgpt"
	for _, root := range []string{"chatgpt.com", "openai.com", "oaistatic.com", "oaiusercontent.com"} {
		if host == root || strings.HasSuffix(host, "."+root) {
			source := "target_domain"
			if app {
				source = "chatgpt_app"
			}
			for _, known := range []string{"chatgpt.com", "ws.chatgpt.com", "android.chat.openai.com", "api.openai.com", "auth.openai.com", "auth0.openai.com"} {
				if host == known {
					return known, source
				}
			}
			return "other." + root, source
		}
	}
	if app {
		return "other_chatgpt_app_destination", "chatgpt_app"
	}
	return "", ""
}

func BeginBusiness(host, process string, inner bool) *BusinessFlow {
	if inner {
		return nil
	}
	b := businessOwner.Load()
	if b == nil {
		return nil
	}
	return b.begin(host, process)
}

func (b *businessRecorder) begin(host, process string) *BusinessFlow {
	target, source := businessTarget(host, process)
	if target == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.active) >= businessFlowLimit {
		b.dropped++
		return nil
	}
	b.sequence++
	now := b.j.now()
	id := b.j.ID(fmt.Sprintf("flow:%s:%d", b.session, b.sequence))
	f := &BusinessFlow{owner: b, started: now, lastSnapshot: now, event: BusinessEvent{ID: id, Target: target, Source: source}}
	b.active[id] = f
	f.emit("connection_started", "", "")
	return f
}

func WithBusiness(ctx context.Context, f *BusinessFlow) context.Context {
	if f == nil {
		return ctx
	}
	return context.WithValue(ctx, businessContextKey{}, f)
}

func BusinessFromContext(ctx context.Context) *BusinessFlow {
	f, _ := ctx.Value(businessContextKey{}).(*BusinessFlow)
	return f
}

func (b *businessRecorder) label(name string) string {
	if name == "IP1" || name == "IP2" || name == "出口" || name == "DIRECT" || name == "REJECT" || name == "PASS" || safeNodeLabel.MatchString(name) {
		return name
	}
	return "节点-" + b.j.ID(name)[:8]
}

func BusinessError(err error) string {
	if err == nil || errors.Is(err, io.EOF) {
		return "eof"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return "timeout"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return "connection_reset"
	}
	var errno syscall.Errno
	if runtime.GOOS == "windows" && errors.As(err, &errno) {
		switch int(errno) {
		case 10054:
			return "connection_reset"
		case 10061:
			return "connection_refused"
		case 10060:
			return "timeout"
		case 10053:
			return "connection_aborted"
		}
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "connection_refused"
	}
	if errors.Is(err, syscall.EPIPE) {
		return "broken_pipe"
	}
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return "closed"
	}
	return "other_io_error"
}

func (f *BusinessFlow) snapshot(now time.Time) BusinessEvent {
	e := f.event
	e.Time = now.UTC()
	e.ElapsedMs = now.Sub(f.started).Milliseconds()
	e.LastUpload, e.LastDownload = f.lastUpload, f.lastDownload
	if !f.waitingSince.IsZero() {
		e.WaitingMs = now.Sub(f.waitingSince).Milliseconds()
	}
	e.Chain = append([]string(nil), e.Chain...)
	return e
}

func (f *BusinessFlow) emit(event, direction, reason string) {
	e := f.snapshot(f.owner.j.now())
	e.Event, e.Direction, e.Reason = event, direction, reason
	if len(f.owner.pending) >= businessQueueLimit {
		f.owner.dropped++
		return
	}
	f.owner.pending = append(f.owner.pending, e)
}

func (f *BusinessFlow) DialAttempt(route BusinessRoute, name string) {
	if f == nil {
		return
	}
	b := f.owner
	b.mu.Lock()
	defer b.mu.Unlock()
	if f.closed {
		return
	}
	route.Label = b.label(name)
	if !validID(route.Group) || !validID(route.Node) {
		return
	}
	if route.Protocol != "Http" && route.Protocol != "Socks5" {
		route.Protocol = "other_proxy"
	}
	f.event.Route = route
	f.seenEpoch, f.seenGeneration = route.Epoch, route.Generation
	f.event.Attempt++
	f.emit("proxy_dial_started", "", "")
}

func (f *BusinessFlow) DialResult(err error, elapsed time.Duration) {
	if f == nil {
		return
	}
	b := f.owner
	b.mu.Lock()
	defer b.mu.Unlock()
	if f.closed {
		return
	}
	reason, event := "", "proxy_dial_ready"
	if err != nil {
		reason, event = BusinessError(err), "proxy_dial_failed"
	}
	previous := len(b.pending)
	f.emit(event, "", reason)
	if len(b.pending) > previous {
		b.pending[len(b.pending)-1].DialMs = elapsed.Milliseconds()
	}
}

func (f *BusinessFlow) Open(chain []string, initialUpload int64) {
	if f == nil {
		return
	}
	b := f.owner
	b.mu.Lock()
	defer b.mu.Unlock()
	if f.closed {
		return
	}
	f.connected = true
	for _, name := range chain[:min(len(chain), 12)] {
		f.event.Chain = append(f.event.Chain, b.label(name))
	}
	f.emit("connection_ready", "", "")
	f.bytes(true, initialUpload)
}

func (f *BusinessFlow) bytes(upload bool, n int64) {
	if n <= 0 || f.closed {
		return
	}
	now := f.owner.j.now()
	if upload {
		f.event.Upload += n
		f.lastUpload = now
		if f.waitingSince.IsZero() {
			f.waitingSince = now
			f.warned = false
		}
		if !f.firstUpload {
			f.firstUpload = true
			f.emit("first_upload", "upload", "")
		}
	} else {
		f.event.Download += n
		f.lastDownload = now
		if f.warned {
			f.emit("downstream_resumed", "download", "")
		}
		f.waitingSince = time.Time{}
		f.warned = false
		if !f.firstDownload {
			f.firstDownload = true
			f.emit("first_download", "download", "")
		}
	}
}

func (f *BusinessFlow) Bytes(upload bool, n int64) {
	if f == nil || n <= 0 {
		return
	}
	b := f.owner
	b.mu.Lock()
	defer b.mu.Unlock()
	f.bytes(upload, n)
}

type businessAnomalyObserver struct {
	observe func(BusinessRoute, string)
}

var businessAnomaly atomic.Pointer[businessAnomalyObserver]

func RegisterBusinessAnomalyObserver(observe func(BusinessRoute, string)) {
	businessAnomaly.Store(&businessAnomalyObserver{observe: observe})
}

func transportAnomaly(reason string) bool {
	return reason == "timeout" || reason == "connection_reset" || reason == "broken_pipe" || reason == "connection_aborted"
}

func (f *BusinessFlow) CopyEnded(direction string, err error) {
	if f == nil {
		return
	}
	b := f.owner
	b.mu.Lock()
	if f.closed || (direction != "upload" && direction != "download") {
		b.mu.Unlock()
		return
	}
	reason := BusinessError(err)
	f.emit("copy_ended", direction, reason)
	route, connected := f.event.Route, f.connected
	b.mu.Unlock()
	if observer := businessAnomaly.Load(); observer != nil && observer.observe != nil && connected && transportAnomaly(reason) {
		observer.observe(route, reason)
	}
}

func (f *BusinessFlow) Failed(stage string, err error) {
	if f == nil {
		return
	}
	b := f.owner
	b.mu.Lock()
	defer b.mu.Unlock()
	if f.closed {
		return
	}
	if stage != "metadata_failed" && stage != "connection_dial_failed" && stage != "retry_failed" {
		stage = "connection_dial_failed"
	}
	f.emit(stage, "", BusinessError(err))
}

func (f *BusinessFlow) Finish() {
	if f == nil {
		return
	}
	b := f.owner
	b.mu.Lock()
	defer b.mu.Unlock()
	if f.closed {
		return
	}
	f.emit("connection_closed", "", "")
	f.closed = true
	delete(b.active, f.event.ID)
}

func (f *BusinessFlow) SelectedProxy(name, process string) {
	if f == nil {
		return
	}
	b := f.owner
	b.mu.Lock()
	defer b.mu.Unlock()
	if f.closed {
		return
	}
	f.event.RootProxy = b.label(name)
	if process == "com.openai.chatgpt" {
		f.event.Source = "chatgpt_app"
	}
	f.emit("route_selected", "", "")
}

func (j *Journal) MarkBusiness(group string) {
	b := j.business
	b.mu.Lock()
	for _, f := range b.active {
		if f.event.Route.Group == group || f.event.Route.Group == "" {
			f.emit("user_mark", "", "")
		}
	}
	b.mu.Unlock()
	_ = b.flush(true)
}

func (j *Journal) BusinessTick(group string, state Snapshot) {
	b := j.business
	b.mu.Lock()
	now := j.now()
	for _, f := range b.active {
		if f.connected && f.event.Route.Group == group && (f.seenEpoch != state.Epoch || f.seenGeneration != state.Generation) {
			f.event.CurrentNode, f.event.CurrentEpoch, f.event.CurrentGeneration = state.Primary, state.Epoch, state.Generation
			changed := f.seenEpoch != state.Epoch || f.event.Route.Node != state.Primary
			f.seenEpoch, f.seenGeneration = state.Epoch, state.Generation
			if changed {
				f.emit("old_connection_after_route_change", "", "")
			}
		}
		if f.connected && !f.waitingSince.IsZero() && now.Sub(f.waitingSince) >= 8*time.Second && !f.warned {
			f.warned = true
			f.emit("no_downstream_observed", "", "observation_only")
		}
		if now.Sub(f.lastSnapshot) >= 30*time.Second {
			f.lastSnapshot = now
			f.emit("connection_snapshot", "", "")
		}
	}
	b.mu.Unlock()
	_ = b.flush(false)
}

func (b *businessRecorder) flush(force bool) error {
	b.flushMu.Lock()
	defer b.flushMu.Unlock()
	b.mu.Lock()
	now := b.j.now()
	if !force && now.Sub(b.lastFlush) < time.Second {
		b.mu.Unlock()
		return nil
	}
	batch := b.pending
	b.pending = nil
	b.lastFlush = now
	b.mu.Unlock()
	for index, event := range batch {
		data, _ := json.Marshal(event)
		if err := b.write(append(data, '\n')); err != nil {
			b.mu.Lock()
			b.dropped += len(batch) - index
			b.mu.Unlock()
			return err
		}
	}
	return nil
}

func (b *businessRecorder) write(data []byte) error {
	path := filepath.Join(b.j.dir, "chatgpt.0.jsonl")
	if info, err := os.Stat(path); err == nil && info.Size()+int64(len(data)) > businessLogLimit {
		for i := 3; i > 0; i-- {
			dst := filepath.Join(b.j.dir, fmt.Sprintf("chatgpt.%d.jsonl", i))
			src := filepath.Join(b.j.dir, fmt.Sprintf("chatgpt.%d.jsonl", i-1))
			if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
				return err
			}
			if err := os.Rename(src, dst); err != nil && !os.IsNotExist(err) {
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

func (b *businessRecorder) export() ([]BusinessEvent, []BusinessEvent, int, error) {
	if err := b.flush(true); err != nil {
		return nil, nil, 0, err
	}
	b.flushMu.Lock()
	defer b.flushMu.Unlock()
	b.mu.Lock()
	active := make([]BusinessEvent, 0, len(b.active))
	for _, f := range b.active {
		e := f.snapshot(b.j.now())
		e.Event = "active_at_export"
		active = append(active, e)
	}
	dropped := b.dropped
	b.mu.Unlock()
	sort.Slice(active, func(i, j int) bool { return active[i].ID < active[j].ID })
	entries := []BusinessEvent{}
	cutoff := b.j.now().Add(-30 * time.Minute)
	for i := 3; i >= 0; i-- {
		f, err := os.Open(filepath.Join(b.j.dir, fmt.Sprintf("chatgpt.%d.jsonl", i)))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, nil, dropped, err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 4096), 16384)
		for scanner.Scan() {
			var e BusinessEvent
			if json.Unmarshal(scanner.Bytes(), &e) != nil || !validBusinessEvent(e) {
				dropped++
				continue
			}
			if !e.Time.Before(cutoff) {
				entries = append(entries, e)
			}
		}
		err = scanner.Err()
		f.Close()
		if err != nil {
			return nil, nil, dropped, err
		}
	}
	return entries, active, dropped, nil
}

func validBusinessEvent(e BusinessEvent) bool {
	allowed := map[string]bool{"connection_started": true, "proxy_dial_started": true, "proxy_dial_ready": true, "proxy_dial_failed": true, "connection_ready": true, "first_upload": true, "first_download": true, "downstream_resumed": true, "copy_ended": true, "metadata_failed": true, "connection_dial_failed": true, "retry_failed": true, "connection_closed": true, "old_connection_after_route_change": true, "no_downstream_observed": true, "connection_snapshot": true, "route_selected": true, "user_mark": true}
	target, source := businessTarget(e.Target, "")
	if e.Target == "other_chatgpt_app_destination" && e.Source == "chatgpt_app" {
		target, source = e.Target, e.Source
	}
	if target != e.Target || source == "" || (e.Source != "target_domain" && e.Source != "chatgpt_app") || !validID(e.ID) || !allowed[e.Event] {
		return false
	}
	for _, id := range []string{e.Route.Group, e.Route.Node, e.CurrentNode} {
		if id != "" && !validID(id) {
			return false
		}
	}
	if e.Direction != "" && e.Direction != "upload" && e.Direction != "download" {
		return false
	}
	for _, name := range append(append([]string(nil), e.Chain...), e.Route.Label, e.RootProxy) {
		if name == "" || name == "IP1" || name == "IP2" || name == "出口" || name == "DIRECT" || name == "REJECT" || name == "PASS" || safeNodeLabel.MatchString(name) {
			continue
		}
		if !anonymousNodeLabel.MatchString(name) {
			return false
		}
	}
	if len(e.Chain) > 12 {
		return false
	}
	switch e.Reason {
	case "", "eof", "cancelled", "timeout", "connection_reset", "connection_aborted", "connection_refused", "broken_pipe", "closed", "other_io_error", "observation_only":
	default:
		return false
	}
	switch e.Route.Protocol {
	case "", "Http", "Socks5", "other_proxy":
	default:
		return false
	}
	return true
}

func businessTimeline(entries, active []BusinessEvent, dropped, truncated int, now time.Time) string {
	var out strings.Builder
	fmt.Fprintf(&out, "ChatGPT 连接诊断（代理侧被动观察）\n导出：%s\n最近 30 分钟；事件 %d 条，当前连接 %d 条，未记录/无效 %d 条，裁剪 %d 条。\n", now.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04:05"), len(entries), len(active), dropped, truncated)
	out.WriteString("自动记录，无需开启详细调试。只观察 TCP，不读取聊天内容，不关闭或重发连接。\n上传后暂无下行不等于 SSL 失败；首个下行字节也不代表 TLS/请求成功。结束方向指数据复制方向，不能单凭它认定谁主动断开。\n换线不迁移旧 TCP；旧连接仍绑定原节点。这里的节点/链路不是本次请求实测出口 IP。\n只有经过 FlClash 的连接可见；绕过 VPN、UDP/QUIC、应用内部 SSL 异常不可见。\n\n")
	labels := map[string]string{"connection_started": "开始连接", "proxy_dial_started": "尝试代理建连", "proxy_dial_ready": "代理建连返回（可能尚需懒握手）", "proxy_dial_failed": "代理建连失败", "connection_ready": "开始转发", "first_upload": "首次上传转发", "first_download": "首次下行转发", "downstream_resumed": "停滞后再次收到下行", "copy_ended": "数据复制结束", "metadata_failed": "域名/路由准备失败", "connection_dial_failed": "最终建连失败", "retry_failed": "一次建连/懒握手重试失败", "connection_closed": "连接已结束", "old_connection_after_route_change": "换线/网络变化后旧连接仍在", "no_downstream_observed": "上传后暂未收到下行（仅观察）", "connection_snapshot": "连接快照", "active_at_export": "导出时仍在", "route_selected": "匹配到代理入口", "user_mark": "用户标记故障时连接"}
	for _, e := range append(append([]BusinessEvent(nil), entries...), active...) {
		route := e.Route.Label
		if len(e.Chain) > 0 {
			route = strings.Join(e.Chain, " → ")
		}
		if route == "" {
			route = e.RootProxy
			if route == "" {
				route = "尚未确定链路"
			}
		}
		fmt.Fprintf(&out, "%s  [%s] %s  %s\n  %s；累计 %dms；上传 %dB / 下行 %dB", e.Time.In(time.FixedZone("CST", 8*3600)).Format("15:04:05.000"), e.ID[:8], e.Target, labels[e.Event], route, e.ElapsedMs, e.Upload, e.Download)
		if e.WaitingMs > 0 {
			fmt.Fprintf(&out, "；上传后 %dms 未见下行", e.WaitingMs)
		}
		if e.DialMs > 0 {
			fmt.Fprintf(&out, "；本次建连 %dms", e.DialMs)
		}
		if e.Direction != "" {
			fmt.Fprintf(&out, "；方向 %s", e.Direction)
		}
		if e.Reason != "" {
			fmt.Fprintf(&out, "；分类 %s", e.Reason)
		}
		if e.Event == "old_connection_after_route_change" {
			fmt.Fprintf(&out, "；原节点 %s / 当前主节点 %s；网络代次 %d→%d；路由代次 %d→%d", e.Route.Node, e.CurrentNode, e.Route.Epoch, e.CurrentEpoch, e.Route.Generation, e.CurrentGeneration)
		}
		out.WriteByte('\n')
	}
	if len(entries) == 0 && len(active) == 0 {
		out.WriteString("没有记录到目标 TCP 连接。请确认新版已运行且 ChatGPT 流量经过 FlClash，出现问题后立即导出。\n")
	}
	return out.String()
}
