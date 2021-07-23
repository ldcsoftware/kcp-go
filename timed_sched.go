package kcp

import (
	"container/heap"
	"fmt"
	"sync"
	"time"
)

const (
	TS_NORMAL = iota
	TS_ONCE
	TS_EXCLUSIVE
	TS_REMOVE
)

type timedFunc struct {
	fnvKey   uint32
	mode     int
	execute  func()
	delayMs  uint32
	expireMs uint32
	index    int
}

// a heap for sorted timed function
type timedFuncHeap []*timedFunc

func (h timedFuncHeap) Len() int {
	return len(h)
}

func (h timedFuncHeap) Less(i, j int) bool {
	return h[i].expireMs < h[j].expireMs
}

func (h timedFuncHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *timedFuncHeap) Push(x interface{}) {
	n := len(*h)
	task := x.(*timedFunc)
	task.index = n
	*h = append(*h, task)
}

func (h *timedFuncHeap) Pop() interface{} {
	old := *h
	n := len(old)
	task := old[n-1]
	old[n-1] = nil
	*h = old[0 : n-1]
	return task
}

// TimedSched represents the control struct for timed parallel scheduler
type TimedSched struct {
	// prepending tasks
	prependTasks    [2][]timedFunc
	prependIdx      int
	prependLock     sync.Mutex
	chPrependNotify chan struct{}

	// tasks will be distributed through chTask
	chTask   chan timedFunc
	keyTimer map[uint32]*timedFunc
	timer    *time.Timer

	taskHeap timedFuncHeap
}

// NewTimedSched creates a parallel-scheduler with given parallelization
func NewTimedSched() *TimedSched {
	ts := new(TimedSched)
	ts.chPrependNotify = make(chan struct{}, 1)
	ts.chTask = make(chan timedFunc)
	ts.keyTimer = make(map[uint32]*timedFunc)
	ts.timer = time.NewTimer(0)

	go ts.sched()
	return ts
}

func (ts *TimedSched) newTasks(tasks []timedFunc) (bool, uint32) {
	var top *timedFunc
	current := currentMs()
	for k := range tasks {
		task := tasks[k]
		tasks[k].execute = nil
		if task.mode == TS_ONCE {
			if _, ok := ts.keyTimer[task.fnvKey]; ok {
				continue
			}
		} else if task.mode == TS_EXCLUSIVE {
			if item, ok := ts.keyTimer[task.fnvKey]; ok {
				heap.Remove(&ts.taskHeap, item.index)
				delete(ts.keyTimer, task.fnvKey)
			}
		} else if task.mode == TS_REMOVE {
			if item, ok := ts.keyTimer[task.fnvKey]; ok {
				heap.Remove(&ts.taskHeap, item.index)
				delete(ts.keyTimer, task.fnvKey)
			}
			continue
		}
		if task.delayMs == 0 || current >= task.expireMs {
			task.execute()
			continue
		}
		heap.Push(&ts.taskHeap, &task)
		if task.mode == TS_ONCE || task.mode == TS_EXCLUSIVE {
			ts.keyTimer[task.fnvKey] = &task
		}
		if task.index == 0 {
			top = &task
		}
	}
	if top != nil {
		var delta uint32
		current = currentMs()
		if top.expireMs > current {
			delta = top.expireMs - current
		}
		return true, delta
	}
	return false, 0
}

func (ts *TimedSched) advanceTasks() (bool, uint32) {
	current := currentMs()
	for ts.taskHeap.Len() > 0 {
		task := ts.taskHeap[0]
		if current >= task.expireMs {
			heap.Pop(&ts.taskHeap).(*timedFunc).execute()
			if task.mode == TS_ONCE || task.mode == TS_EXCLUSIVE {
				delete(ts.keyTimer, task.fnvKey)
			}
		}
		current = currentMs()
		if current < task.expireMs {
			return true, task.expireMs - current
		}
	}
	return false, 0
}

func (ts *TimedSched) sched() {
	drained := false

	for {
		select {
		case <-ts.chPrependNotify:
			ts.prependLock.Lock()
			tasks := ts.prependTasks[ts.prependIdx]
			ts.prependTasks[ts.prependIdx] = ts.prependTasks[ts.prependIdx][:0]
			ts.prependIdx = (ts.prependIdx + 1) % 2
			ts.prependLock.Unlock()
			new, delta := ts.newTasks(tasks)
			if new {
				stopped := ts.timer.Stop()
				if !stopped && !drained {
					<-ts.timer.C
				}
				ts.timer.Reset(time.Duration(delta) * time.Millisecond)
				drained = false
			}
		case <-ts.timer.C:
			drained = true
			new, delta := ts.advanceTasks()
			if new {
				ts.timer.Reset(time.Duration(delta) * time.Millisecond)
				drained = false
			}
		}
	}
}

func (ts *TimedSched) put(fnvkey uint32, mode int, f func(), delayMs, expireMs uint32) {
	ts.prependLock.Lock()
	ts.prependTasks[ts.prependIdx] = append(ts.prependTasks[ts.prependIdx], timedFunc{
		fnvKey:   fnvkey,
		mode:     mode,
		execute:  f,
		delayMs:  delayMs,
		expireMs: expireMs,
	})
	ts.prependLock.Unlock()

	select {
	case ts.chPrependNotify <- struct{}{}:
	default:
	}
}

func (ts *TimedSched) Run(f func(), delayMs uint32) {
	ts.put(0, TS_NORMAL, f, delayMs, currentMs()+delayMs)
}

func (ts *TimedSched) Trace(fnvKey uint32, mode int, f func(), delayMs uint32) {
	if mode != TS_EXCLUSIVE && mode != TS_ONCE {
		panic(fmt.Sprintf("invalid mode:%v", mode))
	}
	ts.put(fnvKey, mode, f, delayMs, currentMs()+delayMs)
}

func (ts *TimedSched) Release(fnvKey uint32) {
	ts.put(fnvKey, TS_REMOVE, nil, 0, 0)
}

type TimedSchedPool struct {
	pool []*TimedSched
}

// NewTimedSched creates a parallel-scheduler with given parallelization
func NewTimedSchedPool(parallel int) *TimedSchedPool {
	pool := make([]*TimedSched, parallel)
	for i := 0; i < parallel; i++ {
		pool[i] = NewTimedSched()
	}
	return &TimedSchedPool{pool: pool}
}

func (p *TimedSchedPool) Pick(fnvKey uint32) *TimedSched {
	return p.pool[fnvKey%uint32(len(p.pool))]
}
