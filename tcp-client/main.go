package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

func checkError(err error) {
	if err != nil {
		fmt.Println("checkError", err)
		os.Exit(-1)
	}
}

type client struct {
	*net.TCPConn
	count          int
	cost           float64
	cost2          float64
	cost3          float64
	readTimes      []float64
	readDelayTimes []float64
}

func echoTester(c *client, msglen, msgcount int) error {
	fmt.Println("echoTester start localAddr", c.LocalAddr(), time.Now(), time.Now().String())

	buf := make([]byte, msglen+20)
	recvBuf := make([]byte, msglen+1024)
	buf[0] = '^'

	for i := 0; i < msgcount; i++ {
		// send packet
		start := time.Now()
		info := strconv.FormatInt(start.UnixNano(), 10) + "."
		copy(buf[msglen:], []byte(info))
		_, err := c.Write(buf)
		if err != nil {
			return err
		}

		// receive packet
		nrecv := 0
		for {
			n, err := c.Read(recvBuf[nrecv:])
			if err != nil {
				return err
			}
			nrecv += n
			if recvBuf[nrecv-1] == '.' {
				if nrecv < msglen {
					fmt.Println("echoTester recv end buf len invalid", nrecv)
				}
				info := string(recvBuf[msglen : nrecv-1])
				strlist := strings.Split(info, "-")

				var upReadTime float64
				i := 0
				readDelayTimes := make([]float64, len(strlist))
				for ; i < len(strlist); i++ {
					first, _ := strconv.ParseFloat(strlist[i], 64)
					if i != 0 {
						readDelayTimes[i-1] = (first - upReadTime) / 1000 / 1000
					}
					upReadTime = first
				}
				readDelayTimes[i-1] = (float64(time.Now().UnixNano()) - upReadTime) / 1000 / 1000

				for i, readDelayTime := range readDelayTimes {
					c.readDelayTimes[i] += readDelayTime
				}
				c.readDelayTimes = c.readDelayTimes[:len(readDelayTimes)]

				costTmp := time.Since(start)
				cost := float64(costTmp.Nanoseconds()) / (1000 * 1000)
				var cost2 float64
				for j := 0; j < i; j++ {
					cost2 += readDelayTimes[j]
				}
				c.cost += cost
				c.cost2 += cost2
				c.count += 1

				break
			}
		}
	}
	return nil
}

func TestClientEcho(clientnum, msgcount, msglen int, remoteAddr string, finish *sync.WaitGroup) {
	clients := make([]*client, clientnum)

	var wg sync.WaitGroup
	wg.Add(clientnum)

	for i := 0; i < clientnum; i++ {
		conn, err := net.Dial("tcp", remoteAddr)
		checkError(err)
		c := &client{
			TCPConn:        conn.(*net.TCPConn),
			readTimes:      make([]float64, 20),
			readDelayTimes: make([]float64, 20),
		}
		clients[i] = c
		go func(j int) {
			time.Sleep(time.Duration(rand.Intn(2000)+2000) * time.Millisecond)
			echoTester(c, msglen, msgcount)
			wg.Done()
		}(i)
	}
	wg.Wait()

	readDelayTimes := make([]float64, 20)

	var avgCostA float64
	var avgCostA2 float64
	echocount := 0
	delayTimesLength := 0
	for _, c := range clients {
		defer c.Close()

		avgCostA += (c.cost / float64(c.count))
		avgCostA2 += (c.cost2 / float64(c.count))
		echocount += c.count

		for i, readDelayTime := range c.readDelayTimes {
			readDelayTimes[i] += readDelayTime / float64(c.count)
		}
		if delayTimesLength == 0 {
			delayTimesLength = len(c.readDelayTimes)
		} else if delayTimesLength != len(c.readDelayTimes) {
			panic("invalid read delay times legnth")
		}
	}

	for i, readDelayTime := range readDelayTimes {
		readDelayTimes[i] = readDelayTime / float64(len(clients))
	}
	readDelayTimes = readDelayTimes[:delayTimesLength]

	avgCost := avgCostA / float64(len(clients))
	avgCost2 := avgCostA2 / float64(len(clients))
	fmt.Printf("TestClientEcho clientnum:%d msgcount:%v msglen:%v echocount:%v remoteAddr:%v avgCost:%v avgCost2:%v \n", clientnum, msgcount, msglen, echocount, remoteAddr, avgCost, avgCost2)
	fmt.Printf("TestClientEcho readDelayTimes:%v \n", readDelayTimes)
	fmt.Println("")

	finish.Done()
}

var clientnum = flag.Int("clientnum", 50, "input client number")
var msgcount = flag.Int("msgcount", 1000, "input msg count")
var msglen = flag.Int("msglen", 100, "input msg length")
var targetAddr = flag.String("targetAddr", "127.0.0.1:7900", "input target address")
var proxyAddr = flag.String("proxyAddr", "127.0.0.1:7890", "input proxy address")
var proxyAddrD = flag.String("proxyAddrD", "127.0.0.1:7891", "input proxy address direct")

func main() {
	flag.Parse()

	fmt.Printf("clientnum:%v\n", *clientnum)
	fmt.Printf("msgcount:%v\n", *msgcount)
	fmt.Printf("msglen:%v\n", *msglen)
	fmt.Printf("targetAddr:%v\n", *targetAddr)
	fmt.Printf("proxyAddr:%v\n", *proxyAddr)
	fmt.Printf("proxyAddrD:%v\n", *proxyAddrD)

	var finish sync.WaitGroup
	finish.Add(3)

	TestClientEcho(*clientnum, *msgcount, *msglen, *proxyAddr, &finish)
	TestClientEcho(*clientnum, *msgcount, *msglen, *proxyAddrD, &finish)
	TestClientEcho(*clientnum, *msgcount, *msglen, *targetAddr, &finish)

	finish.Wait()
}
