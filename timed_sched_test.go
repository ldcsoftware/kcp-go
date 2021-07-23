package kcp

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var deviationMs uint32 = 10

func checkTimeSched(t *testing.T, ts *TimedSched, fnvKey uint32, mode int, f func(), delayMs uint32) {
	start := currentMs()
	tf := func() {
		pass := currentMs() - start
		if pass < delayMs || pass > delayMs+deviationMs {
			fmt.Printf("checkTimeSched delayMs:%v pass:%v \n", delayMs, pass)
			t.FailNow()
		}
		f()
	}
	if mode == TS_NORMAL {
		ts.Run(tf, delayMs)
	} else {
		ts.Trace(fnvKey, mode, tf, delayMs)
	}
}

func checkTimeRemoved(t *testing.T, ts *TimedSched, fnvKey uint32, mode int, f func(), delayMs uint32) {
	tf := func() {
		t.FailNow()
		f()
	}
	if mode == TS_NORMAL {
		ts.Run(tf, delayMs)
	} else {
		ts.Trace(fnvKey, mode, tf, delayMs)
	}
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
	checkTimeRemoved(t, ts, 1, TS_ONCE, fc(4), 20)

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

	checkTimeRemoved(t, ts, 1, TS_ONCE, fc(3), 50)
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
	checkTimeRemoved(t, ts, 1, TS_ONCE, fc(2), 10)
	wg.Wait()

	assert.Equal(t, 1, order[0])
	assert.Nil(t, ts.keyTimer[1])
	assert.Equal(t, 0, len(ts.taskHeap))
	order = order[:0]

	wg.Add(1)
	checkTimeRemoved(t, ts, 1, TS_ONCE, fc(1), 100)
	ts.Release(1)
	checkTimeSched(t, ts, 1, TS_ONCE, fc(2), 10) // 30
	wg.Wait()

	assert.Equal(t, 2, order[0])
}

func TestTimedSchedDelay(t *testing.T) {
	ts := NewTimedSched()
	assert.NotNil(t, ts)

	ch1 := make(chan struct{})

	fc := func(seed int) func() {
		return func() {
			close(ch1)
			time.Sleep(time.Millisecond * 100)
		}
	}
	checkTimeSched(t, ts, 1, TS_NORMAL, fc(1), 10)
	<-ch1

	wg := sync.WaitGroup{}
	f := func() {
		wg.Done()
	}

	oldDeviationMs := deviationMs
	defer func() {
		deviationMs = oldDeviationMs
	}()
	deviationMs += 100

	wg.Add(2)
	checkTimeSched(t, ts, 1, TS_NORMAL, f, 100)
	checkTimeSched(t, ts, 1, TS_NORMAL, f, 10)
	wg.Wait()
}

func randInt(min, max uint32) uint32 {
	return uint32(rand.Intn(int(max-min+1))) + min
}

func normalTSTest(t *testing.T, ts *TimedSched, testCnt int, delayMsMin, delayMsMax uint32, wg *sync.WaitGroup) {
	f := func() {
		wg.Done()
	}
	avgSleepTimeMs := (delayMsMin + delayMsMax) / 2
	for i := 0; i < testCnt; i++ {
		delayMs := randInt(delayMsMin, delayMsMax)
		checkTimeSched(t, ts, 1, TS_NORMAL, f, delayMs)
		time.Sleep(time.Duration(avgSleepTimeMs) * time.Millisecond)
	}
}

func onceTSTest(t *testing.T, ts *TimedSched, testCnt int, fnvKey, delayMsMin, delayMsMax uint32, wg *sync.WaitGroup) {
	f := func() {
		wg.Done()
	}
	avgSleepTimeMs := (delayMsMin + delayMsMax) / 2
	for i := 0; i < testCnt; i++ {
		delayMs := randInt(delayMsMin, delayMsMax)
		if delayMs <= avgSleepTimeMs {
			checkTimeSched(t, ts, fnvKey, TS_ONCE, f, uint32(delayMs))
		} else {
			checkTimeSched(t, ts, fnvKey, TS_ONCE, f, uint32(delayMs))
			checkTimeRemoved(t, ts, fnvKey, TS_ONCE, f, uint32(delayMs))
		}
		time.Sleep(time.Duration(delayMs+deviationMs) * time.Millisecond)
	}
}

func exclusiveTSTest(t *testing.T, ts *TimedSched, testCnt int, fnvKey, delayMsMin, delayMsMax uint32, wg *sync.WaitGroup) {
	f := func() {
		wg.Done()
	}
	avgSleepTimeMs := (delayMsMin + delayMsMax) / 2
	for i := 0; i < testCnt; i++ {
		delayMs := randInt(delayMsMin, delayMsMax)
		if delayMs <= avgSleepTimeMs {
			checkTimeSched(t, ts, fnvKey, TS_EXCLUSIVE, f, uint32(delayMs))
		} else {
			checkTimeRemoved(t, ts, fnvKey, TS_EXCLUSIVE, f, uint32(delayMs))
			checkTimeSched(t, ts, fnvKey, TS_EXCLUSIVE, f, uint32(delayMs))
		}
		time.Sleep(time.Duration(delayMs+deviationMs) * time.Millisecond)
	}
}

func TestTimedSchedConcurrency(t *testing.T) {
	ts := NewTimedSched()
	assert.NotNil(t, ts)

	wg := sync.WaitGroup{}
	parallel := 10
	testCnt := 10
	var delayMsMin uint32 = 0
	var delayMsMax uint32 = 1000

	wg.Add(parallel * testCnt)
	for i := 0; i < parallel; i++ {
		go normalTSTest(t, ts, testCnt, delayMsMin, delayMsMax, &wg)
	}

	wg.Add(parallel * testCnt)
	for i := 0; i < parallel; i++ {
		go onceTSTest(t, ts, testCnt, uint32(i+10), delayMsMin, delayMsMax, &wg)
	}

	wg.Add(parallel * testCnt)
	for i := 0; i < parallel; i++ {
		go exclusiveTSTest(t, ts, testCnt, uint32(i+100), delayMsMin, delayMsMax, &wg)
	}

	wg.Wait()
}
