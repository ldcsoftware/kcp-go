package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	kcp "github.com/ldcsoftware/kcp-go"
	"github.com/urfave/cli"
)

var logs [5]*log.Logger

func init() {
	Debug := log.New(os.Stdout,
		"DEBUG: ",
		log.Ldate|log.Lmicroseconds)

	Info := log.New(os.Stdout,
		"INFO : ",
		log.Ldate|log.Lmicroseconds)

	Warning := log.New(os.Stdout,
		"WARN : ",
		log.Ldate|log.Lmicroseconds)

	Error := log.New(os.Stdout,
		"ERROR: ",
		log.Ldate|log.Lmicroseconds)

	Fatal := log.New(os.Stdout,
		"FATAL: ",
		log.Ldate|log.Lmicroseconds)

	logs = [int(kcp.FATAL) + 1]*log.Logger{Debug, Info, Warning, Error, Fatal}
}

var bufPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 32*1024)
		return &b
	},
}

func checkError(err error) {
	if err != nil {
		kcp.Logf(kcp.ERROR, "checkError: %+v\n", err)
		os.Exit(-1)
	}
}

func toUdpStreamBridge(dst *kcp.UDPStream, src *net.TCPConn) (wcount int, wcost float64, err error) {
	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)

	for {
		start := 0
		n, err := src.Read(*buf)
		if err != nil {
			kcp.Logf(kcp.ERROR, "toUdpStreamBridge reading err:%v n:%v", err, n)
			return wcount, wcost, err
		}

		if n == 0 {
			return 0, 0, nil
		}

		//1
		if (*buf)[n-1] == '.' {
			lastPacketTime := time.Now().UnixNano()

			info := "-" + strconv.FormatInt(lastPacketTime, 10)
			info += "-" + "xxxxxxxxxxxxxxxxxxx" + "-" + "xxxxxxxxxxxxxxxxxxx" + "-" + "xxxxxxxxxxxxxxxxxxx" + "-" + "xxxxxxxxxxxxxxxxxxx" + "."
			copy((*buf)[n-1:], []byte(info))
			n += (len(info) - 1)
		}

		wstart := time.Now()
		_, err = dst.Write((*buf)[start:n])
		wcosttmp := time.Since(wstart)
		wcost += float64(wcosttmp.Nanoseconds()) / (1000 * 1000)
		wcount += 1
		if err != nil {
			kcp.Logf(kcp.ERROR, "toUdpStreamBridge writing err:%v", err)
			return wcount, wcost, err
		}
	}
	return wcount, wcost, nil
}

func toTcpStreamBridge(dst *net.TCPConn, src *kcp.UDPStream) (wcount int, wcost float64, err error) {
	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)

	for {
		n, err := src.Read(*buf)
		if err != nil {
			kcp.Logf(kcp.ERROR, "toTcpStreamBridge reading err:%v n:%v", err, n)
			return wcount, wcost, err
		}

		wstart := time.Now()
		_, err = dst.Write((*buf)[:n])
		wcosttmp := time.Since(wstart)
		wcost += float64(wcosttmp.Nanoseconds()) / (1000 * 1000)
		wcount += 1
		if err != nil {
			kcp.Logf(kcp.ERROR, "toTcpStreamBridge writing err:%v", err)
			return wcount, wcost, err
		}
	}
	return wcount, wcost, nil
}

type TunnelPoll struct {
	tunnels []*kcp.UDPTunnel
	idx     uint32
}

func (poll *TunnelPoll) Add(tunnel *kcp.UDPTunnel) {
	poll.tunnels = append(poll.tunnels, tunnel)
}

func (poll *TunnelPoll) Pick() (tunnel *kcp.UDPTunnel) {
	idx := atomic.AddUint32(&poll.idx, 1) % uint32(len(poll.tunnels))
	return poll.tunnels[idx]
}

type TestSelector struct {
	remoteAddrs []net.Addr
	loopPoll    TunnelPoll
	otherPoll   TunnelPoll
}

func NewTestSelector() (*TestSelector, error) {
	return &TestSelector{}, nil
}

func (sel *TestSelector) Add(tunnel *kcp.UDPTunnel) {
	ip := tunnel.LocalAddr().IP
	if ip.IsLoopback() {
		sel.loopPoll.Add(tunnel)
	} else {
		sel.otherPoll.Add(tunnel)
	}
}

func (sel *TestSelector) Pick(remotes []string) (tunnels []*kcp.UDPTunnel) {
	for i := 0; i < len(remotes); i++ {
		ipstr, _, err := net.SplitHostPort(remotes[i])
		if err != nil {
			panic("pick tunnel")
		}
		ip := net.ParseIP(ipstr)
		if ip.IsLoopback() {
			tunnels = append(tunnels, sel.loopPoll.Pick())
		} else {
			tunnels = append(tunnels, sel.otherPoll.Pick())
		}
	}
	return tunnels
}

// handleClient aggregates connection p1 on mux with 'writeLock'
func handleClient(s *kcp.UDPStream, conn *net.TCPConn) {
	kcp.Logf(kcp.INFO, "handleClient start stream:%v remote:%v", s.GetUUID(), conn.RemoteAddr())
	defer kcp.Logf(kcp.INFO, "handleClient end stream:%v remote:%v", s.GetUUID(), conn.RemoteAddr())

	defer conn.Close()
	defer s.Close()

	shutdown := make(chan struct{}, 2)

	// start tunnel & wait for tunnel termination
	toUDPStream := func(s *kcp.UDPStream, conn *net.TCPConn, shutdown chan struct{}) {
		_, _, err := toUdpStreamBridge(s, conn)
		kcp.Logf(kcp.INFO, "toUDPStream stream:%v remote:%v err:%v", s.GetUUID(), conn.RemoteAddr(), err)
		shutdown <- struct{}{}
	}

	toTCPStream := func(conn *net.TCPConn, s *kcp.UDPStream, shutdown chan struct{}) {
		_, _, err := toTcpStreamBridge(conn, s)
		kcp.Logf(kcp.INFO, "toTCPStream stream:%v remote:%v err:%v", s.GetUUID(), conn.RemoteAddr(), err)
		shutdown <- struct{}{}
	}

	go toUDPStream(s, conn, shutdown)
	go toTCPStream(conn, s, shutdown)

	<-shutdown
}

func main() {
	myApp := cli.NewApp()
	myApp.Name = "tun-client"
	myApp.Version = "1.0.0"
	myApp.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "targetAddr",
			Value: "127.0.0.1:7900",
			Usage: "target address",
		},
		cli.StringFlag{
			Name:  "localIp",
			Value: "127.0.0.1",
			Usage: "local tunnel ip list",
		},
		cli.StringFlag{
			Name:  "localPortS",
			Value: "8001",
			Usage: "local tunnel port start",
		},
		cli.StringFlag{
			Name:  "localPortE",
			Value: "8002",
			Usage: "local tunnel port end",
		},
		cli.StringFlag{
			Name:  "remoteIp",
			Value: "127.0.0.1",
			Usage: "remote tunnel ip",
		},
		cli.StringFlag{
			Name:  "remotePortS",
			Value: "7001",
			Usage: "remote tunnel port start",
		},
		cli.StringFlag{
			Name:  "remotePortE",
			Value: "7002",
			Usage: "remote tunnel port end",
		},
		cli.StringFlag{
			Name:  "transmitTuns",
			Value: "2",
			Usage: "how many tunnels transmit data",
		},
		cli.StringFlag{
			Name:  "mode",
			Value: "fast",
			Usage: "profiles: fast3, fast2, fast, normal, manual",
		},
		cli.IntFlag{
			Name:  "logLevel",
			Value: 2,
			Usage: "kcp log level",
		},
		cli.IntFlag{
			Name:  "wndSize",
			Value: 128,
			Usage: "kcp send/recv wndSize",
		},
		cli.BoolFlag{
			Name:  "ackNoDelay",
			Usage: "stream ackNoDelay",
		},
		cli.IntFlag{
			Name:  "bufferSize",
			Value: 4 * 1024 * 1024,
			Usage: "kcp bufferSize",
		},
		cli.IntFlag{
			Name:  "interval",
			Value: 20,
			Usage: "kcp interval",
		},
		cli.IntFlag{
			Name:  "inputQueueCount",
			Value: 128,
			Usage: "transport inputQueueCount",
		},
		cli.IntFlag{
			Name:  "tunnelProcessorCount",
			Value: 10,
			Usage: "transport tunnelProcessorCount",
		},
		cli.IntFlag{
			Name:  "noResend",
			Value: 0,
			Usage: "kcp noResend",
		},
		cli.IntFlag{
			Name:  "parallelXmit",
			Value: 5,
			Usage: "parallel xmit",
		},
		cli.StringFlag{
			Name:  "telnetAddr",
			Value: "127.0.0.1:23001",
			Usage: "telnet addr",
		},
		cli.IntFlag{
			Name:  "parallelCheckPeriods",
			Value: 0,
			Usage: "parallel check periods",
		},
		cli.Float64Flag{
			Name:  "parallelStreamRate",
			Value: 0,
			Usage: "parallel stream rate",
		},
		cli.IntFlag{
			Name:  "parallelDuration",
			Value: 0,
			Usage: "parallel duration",
		},
	}
	myApp.Action = func(c *cli.Context) error {
		targetAddr := c.String("targetAddr")
		telnetAddr := c.String("telnetAddr")

		localIp := c.String("localIp")
		localIps := strings.Split(localIp, ",")

		lPortS := c.String("localPortS")
		localPortS, err := strconv.Atoi(lPortS)
		checkError(err)
		lPortE := c.String("localPortE")
		localPortE, err := strconv.Atoi(lPortE)
		checkError(err)

		remoteIp := c.String("remoteIp")
		rPortS := c.String("remotePortS")
		remotePortS, err := strconv.Atoi(rPortS)
		checkError(err)
		rPortE := c.String("remotePortE")
		remotePortE, err := strconv.Atoi(rPortE)
		checkError(err)

		transmitTunsS := c.String("transmitTuns")
		transmitTuns, err := strconv.Atoi(transmitTunsS)
		checkError(err)

		logLevel := c.Int("logLevel")
		wndSize := c.Int("wndSize")
		bufferSize := c.Int("bufferSize")
		interval := c.Int("interval")
		inputQueueCount := c.Int("inputQueueCount")
		tunnelProcessorCount := c.Int("tunnelProcessorCount")
		noResend := c.Int("noResend")
		parallelXmit := c.Int("parallelXmit")
		parallelCheckPeriods := c.Int("parallelCheckPeriods")
		parallelStreamRate := c.Float64("parallelStreamRate")
		parallelDuration := time.Second * time.Duration(c.Int("parallelDuration"))

		fmt.Printf("Action targetAddr:%v\n", targetAddr)
		fmt.Printf("Action localIp:%v\n", localIp)
		fmt.Printf("Action localPortS:%v\n", localPortS)
		fmt.Printf("Action localPortE:%v\n", localPortE)
		fmt.Printf("Action remoteIp:%v\n", remoteIp)
		fmt.Printf("Action remotePortS:%v\n", remotePortS)
		fmt.Printf("Action remotePortE:%v\n", remotePortE)
		fmt.Printf("Action transmitTuns:%v\n", transmitTuns)
		fmt.Printf("Action logLevel:%v\n", logLevel)
		fmt.Printf("Action wndSize:%v\n", wndSize)
		fmt.Printf("Action bufferSize:%v\n", bufferSize)
		fmt.Printf("Action interval:%v\n", interval)
		fmt.Printf("Action inputQueueCount:%v\n", inputQueueCount)
		fmt.Printf("Action tunnelProcessorCount:%v\n", tunnelProcessorCount)
		fmt.Printf("Action noResend:%v\n", noResend)
		fmt.Printf("Action parallelXmit:%v\n", parallelXmit)
		fmt.Printf("Action telnetAddr:%v\n", telnetAddr)
		fmt.Printf("Action parallelCheckPeriods:%v\n", parallelCheckPeriods)
		fmt.Printf("Action parallelStreamRate:%v\n", parallelStreamRate)
		fmt.Printf("Action parallelDuration:%v\n", parallelDuration)

		ln, err := net.Listen("tcp", telnetAddr)
		checkError(err)

		kcp.Logf = func(lvl kcp.LogLevel, f string, args ...interface{}) {
			if int(lvl) >= logLevel {
				logs[lvl].Printf(f+"\n", args...)
			}
		}

		kcp.DefaultParallelXmit = parallelXmit

		opt := &kcp.TransportOption{
			AcceptBacklog:        1024,
			DialTimeout:          time.Minute,
			InputQueue:           inputQueueCount,
			TunnelProcessor:      tunnelProcessorCount,
			ParallelCheckPeriods: parallelCheckPeriods,
			ParallelStreamRate:   parallelStreamRate,
			ParallelDuration:     parallelDuration,
		}

		sel, err := NewTestSelector()
		checkError(err)
		transport, err := kcp.NewUDPTransport(sel, opt)
		checkError(err)

		tunnelsm := make(map[string][]*kcp.UDPTunnel)
		for i := 0; i < len(localIps); i++ {
			for portS := localPortS; portS <= localPortE; portS++ {
				tunnel, err := transport.NewTunnel(localIps[i] + ":" + strconv.Itoa(portS))
				checkError(err)
				err = tunnel.SetReadBuffer(bufferSize)
				checkError(err)
				err = tunnel.SetWriteBuffer(bufferSize)
				checkError(err)

				tunnels, ok := tunnelsm[localIps[i]]
				if !ok {
					tunnels = make([]*kcp.UDPTunnel, 0)
				}
				tunnels = append(tunnels, tunnel)
				tunnelsm[localIps[i]] = tunnels
			}
		}

		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					log.Println("Accept failed", err)
					continue
				}
				defer conn.Close()
				scanner := bufio.NewScanner(conn)
				for scanner.Scan() {
					sp := strings.Split(scanner.Text(), " ")
					log.Println("scanner", sp)
					switch sp[0] {
					case "simulate":
						if len(sp) < 2 {
							log.Println("simulate missing param")
							break
						}
						tunnels, ok := tunnelsm[sp[1]]
						if !ok {
							log.Println("simulate invalid addr")
							break
						}
						log.Println("simulate", sp)
						var err error
						var loss float64 = 100
						delayMin := 0
						delayMax := 0
						if len(sp) >= 5 {
							loss, err = strconv.ParseFloat(sp[2], 64)
							checkError(err)
							delayMin, err = strconv.Atoi(sp[3])
							checkError(err)
							delayMax, err = strconv.Atoi(sp[4])
							checkError(err)
						}
						for i := 0; i < len(tunnels); i++ {
							tunnels[i].Simulate(loss, delayMin, delayMax)
						}
					default:
						log.Println("invalid command", sp[0])
					}
				}
			}
		}()

		go func() {
			for {
				return
				time.Sleep(time.Second * 10)
				headers := kcp.DefaultSnmp.Header()
				values := kcp.DefaultSnmp.ToSlice()
				fmt.Printf("------------- snmp result -------------\n")
				for i := 0; i < len(headers); i++ {
					fmt.Printf("snmp header:%v value:%v \n", headers[i], values[i])
				}
				fmt.Printf("------------- snmp result -------------\n\n")
			}
		}()

		for {
			stream, err := transport.Accept()
			checkError(err)
			go func() {
				stream.SetWindowSize(wndSize, wndSize)
				stream.SetNoDelay(kcp.FastStreamOption.Nodelay, interval, kcp.FastStreamOption.Resend, kcp.FastStreamOption.Nc)
				stream.SetParallelXmit(uint32(parallelXmit))
				conn, err := net.Dial("tcp", targetAddr)
				checkError(err)
				tcpConn := conn.(*net.TCPConn)
				go handleClient(stream, tcpConn)
			}()
		}
		return nil
	}

	myApp.Run(os.Args)
}
