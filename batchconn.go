package kcp

import "golang.org/x/net/ipv4"

const (
	batchSize = 64
)

type batchConn interface {
	WriteBatch(ms []ipv4.Message, flags int) (int, error)
	ReadBatch(ms []ipv4.Message, flags int) (int, error)
}
