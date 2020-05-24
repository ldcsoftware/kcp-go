package kcp

import (
	"io"
	"net"
	"sync"

	cmap "github.com/1ucio/concurrent-map"
	"github.com/pkg/errors"
	gouuid "github.com/satori/go.uuid"
)

const (
	// accept backlog
	acceptBacklog = 128
)

var (
	errInvalidRemoteIP = errors.New("invalid remote ip")
)

type RouteSelector interface {
	AddTunnel(tunnel *UDPTunnel)
	Pick(remoteIps []string) (tunnels []*UDPTunnel, remotes []net.Addr)
}

type KCPOption struct {
	nodelay  int
	interval int
	resend   int
	nc       int
}

var defaultKCPOption = &KCPOption{
	nodelay:  1,
	interval: 20,
	resend:   2,
	nc:       1,
}

type UDPTransport struct {
	kcpOption  *KCPOption
	streamm    cmap.ConcurrentMap
	acceptChan chan *UDPStream

	tunnelHostM map[string]*UDPTunnel
	sel         RouteSelector

	die     chan struct{} // notify the listener has closed
	dieOnce sync.Once
}

func NewUDPTransport(sel RouteSelector, option *KCPOption) (t *UDPTransport, err error) {
	if option == nil {
		option = defaultKCPOption
	}
	t = &UDPTransport{
		kcpOption:   option,
		streamm:     cmap.New(),
		acceptChan:  make(chan *UDPStream),
		tunnelHostM: make(map[string]*UDPTunnel),
		sel:         sel,
		die:         make(chan struct{}),
	}
	return t, nil
}

func (t *UDPTransport) NewTunnel(lAddr string) (tunnel *UDPTunnel, err error) {
	tunnel, ok := t.tunnelHostM[lAddr]
	if ok {
		return tunnel, nil
	}

	tunnel, err = NewUDPTunnel(lAddr, func(data []byte, rAddr net.Addr) {
		t.tunnelInput(data, rAddr)
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}
	t.tunnelHostM[lAddr] = tunnel
	t.sel.AddTunnel(tunnel)
	return tunnel, nil
}

func (t *UDPTransport) NewStream(uuid gouuid.UUID, remoteIps []string, accepted bool) (stream *UDPStream, err error) {
	stream, err = NewUDPStream(uuid, remoteIps, t.sel, t, accepted)
	stream.SetNoDelay(t.kcpOption.nodelay, t.kcpOption.interval, t.kcpOption.resend, t.kcpOption.nc)
	if err != nil {
		return nil, err
	}
	return stream, err
}

func (t *UDPTransport) tunnelInput(data []byte, rAddr net.Addr) {
	var uuid gouuid.UUID
	copy(uuid[:], data)
	uuidStr := uuid.String()

	stream, ok := t.streamm.Get(uuidStr)
	if !ok {
		stream = t.streamm.Upsert(uuidStr, nil, func(exist bool, valueInMap interface{}, newValue interface{}) interface{} {
			if exist {
				return valueInMap
			}
			stream, err := t.NewStream(uuid, nil, true)
			if err != nil {
				return nil
			}
			t.acceptChan <- stream
			return stream
		})
	}
	if s, ok := stream.(*UDPStream); ok && s != nil {
		s.input(data)
	}
}

//interface
func (t *UDPTransport) Open(remoteIps []string) (stream *UDPStream, err error) {
	uuid := gouuid.NewV1()
	stream, err = t.NewStream(uuid, remoteIps, false)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	t.streamm.Set(uuid.String(), stream)
	return stream, nil
}

//interface
func (t *UDPTransport) Accept() (stream *UDPStream, err error) {
	select {
	case stream = <-t.acceptChan:
		return stream, nil
	case <-t.die:
		return nil, errors.WithStack(io.ErrClosedPipe)
	}
}
