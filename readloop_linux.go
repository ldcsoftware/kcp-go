// +build linux

package kcp

import (
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	gouuid "github.com/satori/go.uuid"
	"golang.org/x/net/ipv4"
)

// monitor incoming data for all connections of server
func (t *UDPTunnel) readLoop() {
	t.defaultReadLoop()
	// default version
	if t.xconn == nil {
		t.defaultReadLoop()
		return
	}

	// x/net version
	msgs := make([]ipv4.Message, batchSize)
	for k := range msgs {
		msgs[k].Buffers = [][]byte{make([]byte, mtuLimit)}
	}

	for {
		if count, err := t.xconn.ReadBatch(msgs, 0); err == nil {
			for i := 0; i < count; i++ {
				msg := &msgs[i]
				if msg.N >= gouuid.Size+IKCP_OVERHEAD {
					readBuf := msg.Buffers[0][:msg.N]
					if readBuf[len(readBuf)-1] == '.' {
						if len(readBuf) < 21 {
							panic("readLoop wrong msg")
						} else {
							readTime := strconv.FormatInt(time.Now().UnixNano(), 10)
							copy(readBuf[len(readBuf)-20:], []byte(readTime))
						}
					}
					t.input(readBuf, msg.Addr)
				} else {
					atomic.AddUint64(&DefaultSnmp.InErrs, 1)
				}
			}
		} else {
			// compatibility issue:
			// for linux kernel<=2.6.32, support for sendmmsg is not available
			// an error of type os.SyscallError will be returned
			if operr, ok := err.(*net.OpError); ok {
				if se, ok := operr.Err.(*os.SyscallError); ok {
					if se.Syscall == "recvmmsg" {
						t.defaultReadLoop()
						return
					}
				}
			}
			t.notifyReadError(err)
		}
	}
}
