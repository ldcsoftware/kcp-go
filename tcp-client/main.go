package main

import (
	"encoding/binary"
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

var clientnum = flag.Int("clientnum", 50, "input client number")
var msgcount = flag.Int("msgcount", 1000, "input msg count")
var msglen = flag.Int("msglen", 100, "input msg length")
var targetAddr = flag.String("targetAddr", "127.0.0.1:7900", "input target address")
var proxyAddr = flag.String("proxyAddr", "127.0.0.1:7890", "input proxy address")
var proxyAddrD = flag.String("proxyAddrD", "127.0.0.1:7891", "input proxy address direct")
var connectWay = flag.Int("connectWay", 0, "udp proxy, tcp proxy, direct")
var sendIntervalMin = flag.Int("sendIntervalMin", 0, "echo interval min")
var sendIntervalMax = flag.Int("sendIntervalMax", 0, "echo interval max")

type statEle struct {
	all    float64
	avg    float64
	max    float64
	bucket map[int]int
}

type client struct {
	*net.TCPConn
	idx     int
	count   int
	cost    float64
	maxCost time.Duration

	timeStats [][]statEle
}

var eleCount = 4

func echoTester(c *client, msglen, msgcount int) (err error) {
	start := time.Now()
	fmt.Printf("echoTester start c:%v msglen:%v msgcount:%v start:%v\n", c.LocalAddr(), msglen, msgcount, start.Format("2006-01-02 15:04:05.000"))
	defer func() {
		fmt.Printf("echoTester end c:%v msglen:%v msgcount:%v count:%v cost:%v elasp:%v err:%v \n", c.LocalAddr(), msglen, msgcount, c.count, c.cost, time.Since(start), err)
	}()

	buf := make([]byte, msglen)
	binary.LittleEndian.PutUint32(buf, uint32(c.idx))
	buf[len(buf)-1] = '.'
	recvlen := msglen + 20*10
	recvBuf := make([]byte, recvlen)

	for i := 0; i < msgcount; i++ {
		binary.LittleEndian.PutUint32(buf[4:], uint32(i))
		if *sendIntervalMax != 0 {
			delta := *sendIntervalMax - *sendIntervalMin
			interval := rand.Intn(delta+1) + *sendIntervalMin
			time.Sleep(time.Duration(interval) * time.Microsecond)
		}
		// send packet
		start := time.Now()

		errRet := make(chan error, 1)

		go func() {
			// receive packet
			nrecv := 0
			var n int
			for {
				n, err = c.Read(recvBuf[nrecv:])
				if err != nil {
					panic("client read error")
				}
				nrecv += n
				if nrecv == recvlen {
					break
				} else if nrecv > recvlen {
					panic("invalid recv length")
				}
			}
			rClientIdx := binary.LittleEndian.Uint32(buf)
			rMsgIdx := binary.LittleEndian.Uint32(buf[4:])
			if rClientIdx != uint32(c.idx) || rMsgIdx != uint32(i) {
				panic("idx not equal")
			}

			costTmp := time.Since(start)
			cost := float64(costTmp.Nanoseconds()) / (1000 * 1000)
			c.cost += cost
			if costTmp > c.maxCost {
				c.maxCost = costTmp
			}
			c.count += 1

			if recvBuf[recvlen-1] != '.' {
				panic("invalid recv buffer content")
			}

			info := string(recvBuf[msglen : nrecv-1])
			strlist := strings.Split(info, "-")

			if len(strlist) != 10 {
				panic("invalid recv buffer content time")
			}

			timeList := make([]float64, len(strlist))
			for i := 0; i < len(strlist); i++ {
				t := float64(0)
				if strlist[i][0] != 'x' {
					t, _ = strconv.ParseFloat(strlist[i], 64)
					timeList[i] = t
				}
			}

			for j := 0; j < 2; j++ {
				timeStat := c.timeStats[j]

				stats := make([]float64, eleCount)
				timeList = timeList[j*5:]
				stats[0] = (timeList[1] - timeList[0]) / 1000 / 1000
				stats[1] = float64(0)
				if timeList[2] != 0 {
					stats[1] = (timeList[2] - timeList[1]) / 1000 / 1000
					fmt.Printf("change clientIdx:%v msgIdx:%v elasp:%v \n", c.idx, i, stats[1])
				}
				stats[2] = (timeList[3] - timeList[1]) / 1000 / 1000
				if timeList[2] != 0 {
					stats[2] = (timeList[3] - timeList[2]) / 1000 / 1000
				}
				stats[3] = (timeList[4] - timeList[3]) / 1000 / 1000

				for k := 0; k < eleCount; k++ {
					timeStat[k].all += stats[k]
					if stats[k] > timeStat[k].max {
						timeStat[k].max = stats[k]
					}
					bucket := int(stats[k]) / 10
					timeStat[k].bucket[bucket] += 1
				}
			}

			errRet <- nil
		}()

		_, err = c.Write(buf)
		if err != nil {
			return err
		}
		err = <-errRet
		if err != nil {
			return err
		}
	}
	return nil
}

func TestClientEcho(clientnum, msgcount, msglen int, remoteAddr string, finish *sync.WaitGroup) {
	baseSleep := time.Second * 2
	extraSleep := time.Duration(clientnum*3/2) * time.Millisecond

	batch := 100
	batchCount := (clientnum + batch - 1) / batch
	batchSleep := extraSleep / time.Duration(batchCount)

	clients := make([]*client, clientnum)

	fmt.Printf("TestClientEcho clientnum:%d batchCount:%v batchSleep:%v \n", clientnum, batchCount, batchSleep)

	var wg sync.WaitGroup
	wg.Add(clientnum)

	for i := 0; i < batchCount; i++ {
		batchNum := batch
		if i == batchCount-1 {
			batchNum = clientnum - i*batch
		}

		fmt.Printf("TestClientEcho clientnum:%d batchIdx:%v batchNum:%v \n", clientnum, i, batchNum)

		for j := 0; j < batchNum; j++ {
			go func(batchIdx, idx int) {
				conn, err := net.Dial("tcp", remoteAddr)
				checkError(err)

				timeStats := make([][]statEle, 2)
				for m := 0; m < 2; m++ {
					for n := 0; n < eleCount; n++ {
						timeStats[m] = append(timeStats[m], statEle{bucket: make(map[int]int)})
					}
				}

				c := &client{
					idx:       batchIdx*batchCount + idx,
					TCPConn:   conn.(*net.TCPConn),
					timeStats: timeStats,
				}
				clients[batchIdx*batch+idx] = c
				time.Sleep(baseSleep + extraSleep - time.Duration(batchIdx)*batchSleep)
				echoTester(c, msglen, msgcount)
				wg.Done()
			}(i, j)
		}
		time.Sleep(batchSleep)
	}
	wg.Wait()

	var avgCostA float64
	var maxCost time.Duration

	timeStats := make([][]statEle, 2)
	for m := 0; m < 2; m++ {
		for n := 0; n < eleCount; n++ {
			timeStats[m] = append(timeStats[m], statEle{bucket: make(map[int]int)})
		}
	}

	echocount := 0
	for _, c := range clients {
		if c.count != 0 {
			avgCostA += (c.cost / float64(c.count))
			echocount += c.count
			if c.maxCost > maxCost {
				maxCost = c.maxCost
			}
			for i := 0; i < len(c.timeStats); i++ {
				for j := 0; j < len(c.timeStats[i]); j++ {
					cstat := c.timeStats[i][j]
					timeStats[i][j].all += (cstat.all / float64(c.count))
					if cstat.max > timeStats[i][j].max {
						timeStats[i][j].max = cstat.max
					}
					for k, v := range cstat.bucket {
						timeStats[i][j].bucket[k] += v
					}
				}
			}
		}
		c.Close()
	}
	avgCost := avgCostA / float64(len(clients))

	fmt.Printf("TestClientEcho clientnum:%d msgcount:%v msglen:%v echocount:%v remoteAddr:%v avgCost:%v maxCost:%v\n", clientnum, msgcount, msglen, echocount, remoteAddr, avgCost, maxCost)

	for i := 0; i < len(timeStats); i++ {
		timeStat := timeStats[i]
		for j := 0; j < len(timeStat); j++ {
			timeStat[j].avg = timeStat[j].all / float64(len(clients))
		}
	}

	for i := 0; i < len(timeStats); i++ {
		timeStat := timeStats[i]

		sendDelay := timeStat[0]
		changeDealy := timeStat[1]
		recvDelay := timeStat[2]
		readDelay := timeStat[3]

		fmt.Println()

		fmt.Printf("TestClientEcho sendDelay %+v \n", sendDelay)
		fmt.Printf("TestClientEcho changeDealy %+v \n", changeDealy)
		fmt.Printf("TestClientEcho recvDelay %+v \n", recvDelay)
		fmt.Printf("TestClientEcho readDelay %+v \n", readDelay)
		fmt.Println()

	}

	if finish != nil {
		finish.Done()
	}
}

func main() {
	flag.Parse()

	fmt.Printf("clientnum:%v\n", *clientnum)
	fmt.Printf("msgcount:%v\n", *msgcount)
	fmt.Printf("msglen:%v\n", *msglen)
	fmt.Printf("targetAddr:%v\n", *targetAddr)
	fmt.Printf("proxyAddr:%v\n", *proxyAddr)
	fmt.Printf("proxyAddrD:%v\n", *proxyAddrD)
	fmt.Printf("connectWay:%v\n", *connectWay)
	fmt.Printf("sendIntervalMin:%v\n", *sendIntervalMin)
	fmt.Printf("sendIntervalMax:%v\n", *sendIntervalMax)

	if *connectWay == 1 {
		TestClientEcho(*clientnum, *msgcount, *msglen, *proxyAddr, nil)
	} else if *connectWay == 2 {
		TestClientEcho(*clientnum, *msgcount, *msglen, *proxyAddrD, nil)
	} else if *connectWay == 3 {
		TestClientEcho(*clientnum, *msgcount, *msglen, *targetAddr, nil)
	} else {
		var finish sync.WaitGroup
		finish.Add(3)
		TestClientEcho(*clientnum, *msgcount, *msglen, *proxyAddr, &finish)
		time.Sleep(time.Second * 5)
		TestClientEcho(*clientnum, *msgcount, *msglen, *proxyAddrD, &finish)
		time.Sleep(time.Second * 5)
		TestClientEcho(*clientnum, *msgcount, *msglen, *targetAddr, &finish)
		finish.Wait()
	}
}
