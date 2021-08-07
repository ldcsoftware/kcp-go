package kcp

import (
	"sync/atomic"

	"golang.org/x/net/ipv4"
)

func (t *UDPTunnel) writeSingle(msgs []ipv4.Message) {
	nbytes := 0
	npkts := 0
	for k := range msgs {
		if n, err := t.conn.WriteTo(msgs[k].Buffers[0], msgs[k].Addr); err == nil {
			nbytes += n
			npkts++
		} else {
			t.notifyWriteError(err)
		}
	}

	atomic.AddUint64(&DefaultSnmp.WriteCount, uint64(1))
	atomic.AddUint64(&DefaultSnmp.OutPkts, uint64(npkts))
	atomic.AddUint64(&DefaultSnmp.OutBytes, uint64(nbytes))
}

func (t *UDPTunnel) defaultWriteLoop() {
	var msgs []ipv4.Message
	for {
		select {
		case <-t.die:
			return
		default:
		}

		queue := t.broker.Acquire(&msgs)
		t.writeSingle(msgs)
		t.broker.Release(queue, msgs)
		msgs = msgs[:0]
	}
}
