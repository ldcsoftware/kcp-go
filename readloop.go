package kcp

import (
	"strconv"
	"sync/atomic"
	"time"

	gouuid "github.com/satori/go.uuid"
)

func (t *UDPTunnel) defaultReadLoop() {
	buf := make([]byte, mtuLimit)
	for {
		if n, from, err := t.conn.ReadFrom(buf); err == nil {
			if n >= gouuid.Size+IKCP_OVERHEAD {
				readBuf := buf[:n]
				if readBuf[len(readBuf)-1] == '.' {
					if len(readBuf) < 21 {
						panic("readLoop wrong msg")
					} else {
						readTime := strconv.FormatInt(time.Now().UnixNano(), 10)
						copy(readBuf[len(readBuf)-20:], []byte(readTime))
					}
				}
				t.input(buf[:n], from)
			} else {
				atomic.AddUint64(&DefaultSnmp.InErrs, 1)
			}
		} else {
			t.notifyReadError(err)
		}
	}
}
