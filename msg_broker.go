package kcp

import (
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
	limit   *BlockingCountLimit
	msgqs   []*MsgQueue
	msgqIdx int64
}

func NewMsgBroker(limitCnt, queueCnt int) *MsgBroker {
	if limitCnt == 0 {
		limitCnt = DefaultLimitCount
	}
	if queueCnt == 0 {
		queueCnt = DefaultQueueCount
	}
	limit := NewBlockingCount(limitCnt)

	msgqs := make([]*MsgQueue, queueCnt)
	for i := 0; i < len(msgqs); i++ {
		msgqs[i] = &MsgQueue{}
	}
	return &MsgBroker{
		limit: limit,
		msgqs: msgqs,
	}
}

func (p *MsgBroker) Push(msgs []ipv4.Message) (err error) {
	msgqIdx := atomic.AddInt64(&p.msgqIdx, 1)
	msgq := p.msgqs[msgqIdx%int64(len(p.msgqs))]
	msgq.mu.Lock()
	msgq.msgss[msgq.wIdx] = append(msgq.msgss[msgq.wIdx], msgs...)
	msgq.mu.Unlock()
	return nil
}

func (p *MsgBroker) Pop(msgs *[]ipv4.Message) {
	for _, msgq := range p.msgqs {
		msgq.mu.Lock()
		msgsTmp := msgq.msgss[msgq.wIdx]
		msgq.wIdx = (msgq.wIdx + 1) % 2
		msgq.msgss[msgq.wIdx] = msgq.msgss[msgq.wIdx][:0]
		msgq.mu.Unlock()
		if len(msgsTmp) != 0 {
			*msgs = append(*msgs, msgsTmp...)
		}
	}
}

func (p *MsgBroker) Free(msgs []ipv4.Message) {
	for _, msg := range msgs {
		xmitBuf.Put(msg.Buffers[0])
		msg.Buffers = nil
	}
}

func (p *MsgBroker) Acquire(msgs *[]ipv4.Message) {
	p.limit.Acquire()
	p.Pop(msgs)
}

func (p *MsgBroker) Release(msgs []ipv4.Message) {
	p.limit.Release()
	p.Free(msgs)
}
