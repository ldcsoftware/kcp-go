// +build linux

package kcp

import (
	"runtime"
	"net"
	"os"
	"sync/atomic"

	"golang.org/x/net/ipv4"
)

func (t *UDPTunnel) writeBatch(msgs []ipv4.Message) {
	// x/net version
	nbytes := 0
	npkts := 0

	for len(msgs) > 0 {
		if n, err := t.xconn.WriteBatch(msgs, 0); err == nil {
			for k := range msgs[:n] {
				nbytes += len(msgs[k].Buffers[0])
			}
			npkts += n
			msgs = msgs[n:]
		} else {
			// compatibility issue:
			// for linux kernel<=2.6.32, support for sendmmsg is not available
			// an error of type os.SyscallError will be returned
			if operr, ok := err.(*net.OpError); ok {
				if se, ok := operr.Err.(*os.SyscallError); ok {
					if se.Syscall == "sendmmsg" {
						t.xconnWriteError = se
						t.writeSingle(msgs)
						return
					}
				}
			}
			t.notifyWriteError(err)
		}
	}

	atomic.AddUint64(&DefaultSnmp.OutPkts, uint64(npkts))
	atomic.AddUint64(&DefaultSnmp.OutBytes, uint64(nbytes))
}

func (t *UDPTunnel) writeLoop() {
	// default version
	if t.xconn == nil || t.xconnWriteError != nil {
		t.defaultWriteLoop()
		return
	}

	var msgs []ipv4.Message
	for {
		select {
		case <-t.die:
			return
		default:
		}

		t.popMsgs(&msgs)
		t.writeBatch(msgs)
		t.releaseMsgs(msgs)

		if len(msgs) == 0 {
			runtime.Gosched()
		} else {
			msgs = msgs[:0]
		}
	}
}
