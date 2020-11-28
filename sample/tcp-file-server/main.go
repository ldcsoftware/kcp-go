package main

import (
	"crypto/md5"
	"flag"
	"io"
	"log"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"
)

var bufPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 32*1024)
		return &b
	},
}

func iobridge(src io.Reader, dst io.Writer) {
	buf := bufPool.Get().(*[]byte)
	for {
		n, err := src.Read(*buf)
		if err != nil {
			log.Printf("iobridge reading err:%v n:%v \n", err, n)
			break
		}

		_, err = dst.Write((*buf)[:n])
		if err != nil {
			log.Printf("iobridge writing err:%v \n", err)
			break
		}
	}
	bufPool.Put(buf)

	log.Printf("iobridge end \n")
}

type fileToStream struct {
	src  io.Reader
	conn net.Conn
}

func (fs *fileToStream) Read(b []byte) (n int, err error) {
	n, err = fs.src.Read(b)
	if err == io.EOF {
		if tcpconn, ok := fs.conn.(*net.TCPConn); ok {
			tcpconn.CloseWrite()
		}
	}
	return n, err
}

type fileSizeWrite struct {
	size int
}

func (fsw *fileSizeWrite) Write(p []byte) (n int, err error) {
	fsw.size += len(p)
	return len(p), nil
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func randString(n int) string {
	rand.Seed(time.Now().UnixNano())
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func TestTCPFileTransfer(conn net.Conn, size int) {
	defer conn.Close()

	file := randString(size)
	reader := strings.NewReader(file)

	lh := md5.New()
	_, err := io.Copy(lh, reader)
	hash := lh.Sum(nil)
	log.Printf("TestTCPFileTransfer fileLen:%v hash:%v err:%v \n", len(file), hash, err)
	reader.Seek(0, io.SeekStart)

	shutdown := make(chan bool, 2)

	//send file
	go func() {
		fs := &fileToStream{reader, conn}
		iobridge(fs, conn)
		log.Printf("FileTransfer file send finish. hash:%v \n", hash)

		shutdown <- true
	}()

	//recv file
	go func() {
		h := md5.New()
		fsw := &fileSizeWrite{}
		mw := io.MultiWriter(h, fsw)

		iobridge(conn, mw)
		recvHash := h.Sum(nil)

		log.Printf("FileTransfer file recv finish. size:%v hash:%v \n", fsw.size, recvHash)

		shutdown <- true
	}()

	<-shutdown
	<-shutdown

	log.Printf("FileTransfer finish \n")
	time.Sleep(time.Second * 5)
}

var addr = flag.String("addr", "127.0.0.1:7900", "input listen address")
var size = flag.Int("size", 1024*1024*100, "file size")

func main() {
	flag.Parse()
	log.Printf("listen addr:%v size:%v \n", *addr, *size)

	l, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("Listen failed. err: %v \n", err)
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			log.Fatalf("FileTransferServer Accept stream %v \n", err)
		}
		go TestTCPFileTransfer(conn, *size)
	}
}
