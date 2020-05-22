// Package kcp-go is a Reliable-UDP library for golang.
//
// This library intents to provide a smooth, resilient, ordered,
// error-checked and anonymous delivery of streams over UDP packets.
//
// The interfaces of this package aims to be compatible with
// net.Conn in standard library, but offers powerful features for advanced users.
package kcp

import (
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/pkg/errors"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

var (
	errInvalidOperation = errors.New("invalid operation")
)

type input_callback func(data []byte, addr net.Addr)

type (
	// UDPTunnel defines a KCP session implemented by UDP
	UDPTunnel struct {
		conn net.PacketConn // the underlying packet connection

		// notifications
		die     chan struct{} // notify current session has Closed
		dieOnce sync.Once

		// socket error handling
		socketReadError      atomic.Value
		socketWriteError     atomic.Value
		chSocketReadError    chan struct{}
		chSocketWriteError   chan struct{}
		socketReadErrorOnce  sync.Once
		socketWriteErrorOnce sync.Once

		// packets waiting to be sent on wire
		txqueues        [][]ipv4.Message
		xconn           batchConn // for x/net
		xconnWriteError error

		mu    sync.Mutex
		input input_callback
	}
)

// newUDPSession create a new udp session for client or server
func NewUDPTunnel(laddr string, input input_callback) (tunnel *UDPTunnel, err error) {
	// network type detection
	lUDPAddr, err := net.ResolveUDPAddr("udp", laddr)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	network := "udp4"
	if lUDPAddr.IP.To4() == nil {
		network = "udp"
	}

	conn, err := net.ListenUDP(network, lUDPAddr)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	tunnel = new(UDPTunnel)
	tunnel.chSocketReadError = make(chan struct{})
	tunnel.chSocketWriteError = make(chan struct{})
	tunnel.conn = conn
	tunnel.input = input

	// cast to writebatch conn
	if lUDPAddr.IP.To4() != nil {
		tunnel.xconn = ipv4.NewPacketConn(conn)
	} else {
		tunnel.xconn = ipv6.NewPacketConn(conn)
	}

	go tunnel.readLoop()
	go tunnel.writeLoop()
	return tunnel, nil
}

func (s *UDPTunnel) Output(txqueue []ipv4.Message) (err error) {
	if len(txqueue) == 0 {
		return
	}

	select {
	case <-s.die:
		return errors.WithStack(io.ErrClosedPipe)
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.txqueues = append(s.txqueues, txqueue)
	return
}

func (s *UDPTunnel) Input(data []byte, addr net.Addr) {
	s.input(data, addr)
}

// Close closes the connection.
func (s *UDPTunnel) Close() error {
	var once bool
	s.dieOnce.Do(func() {
		close(s.die)
		once = true
	})

	if once {
		s.mu.Lock()
		s.ReleaseTX(s.txqueues)
		s.txqueues = s.txqueues[:0]
		s.mu.Unlock()

		s.conn.Close()
		return nil
	} else {
		return errors.WithStack(io.ErrClosedPipe)
	}
}

func (s *UDPTunnel) IsClosed() bool {
	select {
	case <-s.die:
		return true
	default:
		return false
	}
}

func (s *UDPTunnel) ReleaseTX(txqueues [][]ipv4.Message) {
	for _, txqueue := range txqueues {
		for k := range txqueue {
			xmitBuf.Put(txqueue[k].Buffers[0])
			txqueue[k].Buffers = nil
		}
	}
}

func (s *UDPTunnel) notifyReadError(err error) {
	//!todo
	//read错误，有可能需要直接Close
}

func (s *UDPTunnel) notifyWriteError(err error) {
	//!todo
	//得确认是目标的问题，还是自身的问题
}
