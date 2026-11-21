package outboundgroup

import (
	"context"
	"runtime"
	"sync"
	"time"
)

type stableProbeJob struct {
	group     string
	class     int
	ctx       context.Context
	cancel    context.CancelFunc
	ready     chan struct{}
	started   bool
	preempted bool
	queued    time.Time
	outage    bool
}

type stableProbeDispatch struct {
	mu          sync.Mutex
	limit       int
	outageLimit int
	queue       []*stableProbeJob
	active      map[*stableProbeJob]bool
	served      [3]map[string]uint64
	waiting     map[string]int
	sequence    uint64
	completed   uint64
	preempted   uint64
	maxWait     time.Duration
}

func newStableProbeDispatch(limit int) *stableProbeDispatch {
	d := &stableProbeDispatch{limit: limit, waiting: map[string]int{}, active: map[*stableProbeJob]bool{}}
	for i := range d.served {
		d.served[i] = map[string]uint64{}
	}
	return d
}

var stableBackgroundDispatch = func() *stableProbeDispatch {
	d := newStableProbeDispatch(stableBackgroundLimit())
	d.outageLimit = 8
	if runtime.GOOS == "android" {
		d.outageLimit = 6
	}
	return d
}()

func stableBackgroundLimit() int {
	if runtime.GOOS == "android" {
		return 3
	}
	return 4
}

func (d *stableProbeDispatch) effectiveLimitLocked() int {
	if d.outageLimit <= d.limit {
		return d.limit
	}
	for job := range d.active {
		if job.outage && job.ctx.Err() == nil {
			return d.outageLimit
		}
	}
	for _, job := range d.queue {
		if job.outage && job.ctx.Err() == nil {
			return d.outageLimit
		}
	}
	return d.limit
}

func (d *stableProbeDispatch) businessWait(group string) func() {
	d.mu.Lock()
	d.waiting[group]++
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		d.waiting[group]--
		if d.waiting[group] == 0 {
			delete(d.waiting, group)
		}
		d.mu.Unlock()
	}
}

func outageSearchLimit(limit int) int {
	if limit >= 4 {
		return limit - 2
	}
	if limit > 1 {
		return limit - 1
	}
	return limit
}

func (d *stableProbeDispatch) searchCapacity(group string, outage bool) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	limit := d.effectiveLimitLocked()
	if outage && d.outageLimit > limit {
		limit = d.outageLimit
	}
	count := len(d.active)
	searches := 0
	for job := range d.active {
		if job.class == 2 {
			searches++
		}
	}
	if outage && searches >= outageSearchLimit(limit) {
		return 0
	}
	for _, job := range d.queue {
		if job.ctx.Err() == nil {
			count++
		}
	}
	if count >= limit {
		return 0
	}
	free := limit - count
	if len(d.waiting) > 0 && d.waiting[group] == 0 && free > 1 {
		return 1
	}
	return free
}

func (d *stableProbeDispatch) prefers(job, other *stableProbeJob) bool {
	if job.class != other.class {
		return job.class < other.class
	}
	waiting := job.outage && d.waiting[job.group] > 0
	otherWaiting := other.outage && d.waiting[other.group] > 0
	if waiting != otherWaiting {
		return waiting
	}
	return d.served[job.class][job.group] < d.served[other.class][other.group]
}

func (d *stableProbeDispatch) dispatchLocked() {
	limit := d.effectiveLimitLocked()
	for len(d.active) < limit {
		best := -1
		for i, job := range d.queue {
			if job.ctx.Err() != nil {
				continue
			}
			if job.class == 2 && job.outage {
				optional := 0
				for running := range d.active {
					if running.class == 2 {
						optional++
					}
				}
				if optional >= outageSearchLimit(limit) {
					continue
				}
			}
			if job.class == 1 {
				n := 0
				for running := range d.active {
					if running.class == 1 {
						n++
					}
				}
				if n >= limit-1 {
					continue
				}
			}
			if best < 0 || d.prefers(job, d.queue[best]) {
				best = i
			}
		}
		if best < 0 {
			return
		}
		job := d.queue[best]
		d.queue = append(d.queue[:best], d.queue[best+1:]...)
		job.started = true
		d.active[job] = true
		d.sequence++
		d.served[job.class][job.group] = d.sequence
		if wait := time.Since(job.queued); wait > d.maxWait {
			d.maxWait = wait
		}
		close(job.ready)
	}
}

type admittedSearchKey struct{}

func (d *stableProbeDispatch) trySearch(ctx context.Context, group string, cancel context.CancelFunc, outage bool) (func(), bool) {
	job := &stableProbeJob{group: group, class: 2, ctx: ctx, cancel: cancel, ready: make(chan struct{}), queued: time.Now(), outage: outage}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.queue = append(d.queue, job)
	d.dispatchLocked()
	if !job.started {
		d.queue = d.queue[:len(d.queue)-1]
		return nil, false
	}
	return func() {
		d.mu.Lock()
		delete(d.active, job)
		d.completed++
		d.dispatchLocked()
		d.mu.Unlock()
	}, true
}

func (d *stableProbeDispatch) acquire(ctx context.Context, group string, class int, cancel context.CancelFunc, outage ...bool) (func(), error) {
	job := &stableProbeJob{group: group, class: class, ctx: ctx, cancel: cancel, ready: make(chan struct{}), queued: time.Now()}
	job.outage = len(outage) > 0 && outage[0]
	d.mu.Lock()
	d.queue = append(d.queue, job)
	d.dispatchLocked()
	eligible := true
	if class == 1 {
		confirmations := 0
		for running := range d.active {
			if running.class == 1 {
				confirmations++
			}
		}
		eligible = confirmations < d.effectiveLimitLocked()-1
	}
	if !job.started && class < 2 && eligible {
		for running := range d.active {
			if running.class == 2 && !running.preempted && !(running.outage && class == 1) {
				running.preempted = true
				d.preempted++
				running.cancel()
				break
			}
		}
	}
	d.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			d.mu.Lock()
			delete(d.active, job)
			for i, queued := range d.queue {
				if queued == job {
					d.queue = append(d.queue[:i], d.queue[i+1:]...)
					break
				}
			}
			if job.started {
				d.completed++
			}
			d.dispatchLocked()
			d.mu.Unlock()
		})
	}
	select {
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	case <-job.ready:
		if ctx.Err() != nil {
			release()
			return nil, ctx.Err()
		}
		return release, nil
	}
}

func (d *stableProbeDispatch) snapshot() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	active, queued := [3]int{}, [3]int{}
	for job := range d.active {
		active[job.class]++
	}
	for _, job := range d.queue {
		if job.ctx.Err() == nil {
			queued[job.class]++
		}
	}
	return map[string]any{"limit": d.effectiveLimitLocked(), "normalLimit": d.limit, "outageLimit": d.outageLimit, "runningByPriority": active, "queuedByPriority": queued, "completed": d.completed, "preempted": d.preempted, "maxQueueWaitMs": d.maxWait.Milliseconds()}
}
