package kcp

import (
	"strconv"
	"sync/atomic"
	"time"

	"golang.org/x/net/ipv4"
)

func (t *UDPTunnel) writeSingle(msgs []ipv4.Message) {
	nbytes := 0
	npkts := 0
	for k := range msgs {
		lastBuffer := msgs[k].Buffers[0]
		if lastBuffer[len(lastBuffer)-1] == '.' {
			if len(lastBuffer) < 41 {
				panic("writeBatch wrong msg")
			} else {
				writeTime := strconv.FormatInt(time.Now().UnixNano(), 10)
				copy(lastBuffer[len(lastBuffer)-40:], []byte(writeTime))
			}
		}

		if n, err := t.conn.WriteTo(msgs[k].Buffers[0], msgs[k].Addr); err == nil {
			nbytes += n
			npkts++
		} else {
			t.notifyWriteError(err)
		}
	}

	atomic.AddUint64(&DefaultSnmp.OutPkts, uint64(npkts))
	atomic.AddUint64(&DefaultSnmp.OutBytes, uint64(nbytes))
}

func (t *UDPTunnel) defaultWriteLoop() {
	for {
		select {
		case <-t.die:
			return
		case <-t.chFlush:
		}

		msgss := t.popMsgss()
		for _, msgs := range msgss {
			t.writeSingle(msgs)
		}
		t.releaseMsgss(msgss)
	}
}
