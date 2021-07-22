package kcp

import (
	"math/rand"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

var deviationMs uint32 = 10

func checkTimeSched(t *testing.T, ts *TimedSched, fnvKey uint32, mode int, f func(), delayMs uint32) {
	start := currentMs()
	tf := func() {
		pass := currentMs() - start
		assert.True(t, delayMs <= pass && pass <= delayMs+deviationMs)
		f()
	}
	ts.Put(fnvKey, mode, tf, delayMs)
}

func TestTimedSchedNormal(t *testing.T) {
	ts := NewTimedSched()
	assert.NotNil(t, ts)

	wg := sync.WaitGroup{}
	f := func() {
		wg.Done()
	}

	testCnt := 100
	immediatelyRate := 0.1
	immediatelyRateN := int(1 / immediatelyRate)
	delayMsMax := 1000

	for i := 0; i < testCnt; i++ {
		wg.Add(1)
		imme := rand.Intn(immediatelyRateN)
		if imme == 0 {
			checkTimeSched(t, ts, 1, TS_NORMAL, f, 0)
		} else {
			checkTimeSched(t, ts, 1, TS_NORMAL, f, uint32(rand.Intn(delayMsMax)))
		}
	}

	wg.Wait()
}

func TestTimedSchedOnly(t *testing.T) {
	ts := NewTimedSched()
	assert.NotNil(t, ts)

	wg := sync.WaitGroup{}
	lock := sync.Mutex{}
	order := make([]int, 0)

	fc := func(seed int) func() {
		return func() {
			lock.Lock()
			defer lock.Unlock()

			order = append(order, seed)
			wg.Done()
		}
	}

	wg.Add(4)

	checkTimeSched(t, ts, 1, TS_NORMAL, fc(1), 100) // 100
	checkTimeSched(t, ts, 1, TS_NORMAL, fc(2), 10)  // 10

	checkTimeSched(t, ts, 1, TS_ONCE, fc(3), 50) // 50
	checkTimeSched(t, ts, 1, TS_ONCE, fc(4), 20)

	checkTimeSched(t, ts, 2, TS_ONCE, fc(5), 30) // 30

	wg.Wait()

	assert.Equal(t, 2, order[0])
	assert.Equal(t, 5, order[1])
	assert.Equal(t, 3, order[2])
	assert.Equal(t, 1, order[3])
}

func TestTimedSchedExclusive(t *testing.T) {
	ts := NewTimedSched()
	assert.NotNil(t, ts)

	wg := sync.WaitGroup{}
	lock := sync.Mutex{}
	order := make([]int, 0)

	fc := func(seed int) func() {
		return func() {
			lock.Lock()
			defer lock.Unlock()

			order = append(order, seed)
			wg.Done()
		}
	}

	wg.Add(4)

	checkTimeSched(t, ts, 1, TS_NORMAL, fc(1), 100) // 100
	checkTimeSched(t, ts, 1, TS_NORMAL, fc(2), 10)  // 10

	checkTimeSched(t, ts, 1, TS_ONCE, fc(3), 50)
	checkTimeSched(t, ts, 1, TS_EXCLUSIVE, fc(4), 20) // 20

	checkTimeSched(t, ts, 2, TS_EXCLUSIVE, fc(5), 30) // 30

	wg.Wait()

	assert.Equal(t, 2, order[0])
	assert.Equal(t, 4, order[1])
	assert.Equal(t, 5, order[2])
	assert.Equal(t, 1, order[3])
}

func TestTimedSchedRelease(t *testing.T) {
	ts := NewTimedSched()
	assert.NotNil(t, ts)

	wg := sync.WaitGroup{}
	lock := sync.Mutex{}
	order := make([]int, 0)

	fc := func(seed int) func() {
		return func() {
			lock.Lock()
			defer lock.Unlock()

			order = append(order, seed)
			wg.Done()
		}
	}

	wg.Add(1)
	checkTimeSched(t, ts, 1, TS_ONCE, fc(1), 100)
	checkTimeSched(t, ts, 1, TS_ONCE, fc(2), 10) // 10
	wg.Wait()

	assert.Equal(t, 1, order[0])
	assert.Nil(t, ts.keyTimer[1])
	assert.Equal(t, 0, len(ts.taskHeap))
	order = order[:0]

	wg.Add(1)
	checkTimeSched(t, ts, 1, TS_ONCE, fc(1), 100)
	ts.Release(1)
	checkTimeSched(t, ts, 1, TS_ONCE, fc(2), 10) // 30
	wg.Wait()

	assert.Equal(t, 2, order[0])
}
