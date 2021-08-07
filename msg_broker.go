package kcp

import (
	"runtime"
	"sync"
	"sync/atomic"

	"golang.org/x/net/ipv4"
)

const (
	DefaultLimitCount = 3
	DefaultQueueCount = 10
)

type BlockingCountLimit struct {
	ch chan struct{}
}

func NewBlockingCount(n int) *BlockingCountLimit {
	ch := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		ch <- struct{}{}
	}
	return &BlockingCountLimit{ch: ch}
}

func (l *BlockingCountLimit) Acquire() {
	<-l.ch
}

func (l *BlockingCountLimit) Release() {
	l.ch <- struct{}{}
}

type MsgQueue struct {
	mu    sync.Mutex
	msgss [2][]ipv4.Message
	wIdx  int
}

type MsgBroker struct {
	limit      *BlockingCountLimit
	msgqs      []*MsgQueue
	qToken     chan int
	msgqIdx    int64
	msgPending int64
}

func NewMsgBroker(limitCnt, queueCnt int) *MsgBroker {
	if limitCnt == 0 {
		limitCnt = DefaultLimitCount
	}
	if queueCnt == 0 {
		queueCnt = DefaultQueueCount
	}
	limit := NewBlockingCount(limitCnt)

	qToken := make(chan int, queueCnt)
	for i := 0; i < queueCnt; i++ {
		qToken <- i
	}

	msgqs := make([]*MsgQueue, queueCnt)
	for i := 0; i < len(msgqs); i++ {
		msgqs[i] = &MsgQueue{}
	}
	return &MsgBroker{
		limit:  limit,
		msgqs:  msgqs,
		qToken: qToken,
	}
}

func (p *MsgBroker) Push(msgs []ipv4.Message) (err error) {
	atomic.AddInt64(&p.msgPending, int64(len(msgs)))

	msgqIdx := atomic.AddInt64(&p.msgqIdx, 1)
	msgq := p.msgqs[msgqIdx%int64(len(p.msgqs))]
	msgq.mu.Lock()
	msgq.msgss[msgq.wIdx] = append(msgq.msgss[msgq.wIdx], msgs...)
	msgq.mu.Unlock()
	return nil
}

func (p *MsgBroker) Pop(msgs *[]ipv4.Message) int {
	queue := <-p.qToken
	msgq := p.msgqs[queue]

	msgq.mu.Lock()
	msgsTmp := msgq.msgss[msgq.wIdx]
	msgq.wIdx = (msgq.wIdx + 1) % 2
	msgq.msgss[msgq.wIdx] = msgq.msgss[msgq.wIdx][:0]
	msgq.mu.Unlock()
	if len(msgsTmp) != 0 {
		*msgs = append(*msgs, msgsTmp...)
	}

	atomic.AddInt64(&p.msgPending, -int64(len(*msgs)))
	return queue
}

func (p *MsgBroker) Free(queue int, msgs []ipv4.Message) {
	p.qToken <- queue
	for _, msg := range msgs {
		xmitBuf.Put(msg.Buffers[0])
		msg.Buffers = nil
	}
}

func (p *MsgBroker) Acquire(msgs *[]ipv4.Message) int {
	for atomic.LoadInt64(&p.msgPending) == 0 {
		runtime.Gosched()
	}
	p.limit.Acquire()
	return p.Pop(msgs)
}

func (p *MsgBroker) Release(queue int, msgs []ipv4.Message) {
	p.limit.Release()
	p.Free(queue, msgs)
}
