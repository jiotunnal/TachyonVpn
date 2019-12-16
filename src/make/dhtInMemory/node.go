package dhtInMemory

import (
	"encoding/binary"
	"github.com/tachyon-protocol/udw/udwCryptoSha3"
	"github.com/tachyon-protocol/udw/udwRand"
	"math"
	"sync"
)

type node struct {
	id         uint64
	knownNodes map[uint64]bool
	lock       sync.RWMutex
	keyMap     map[uint64][]byte
}

func newNode(bootstrapNodeIds ...uint64) *node {
	n :=  &node{
		id:     udwRand.MustCryptoRandUint64(),
		keyMap: map[uint64][]byte{},
		knownNodes: map[uint64]bool{},
	}
	for _, id := range bootstrapNodeIds {
		n.knownNodes[id] = true
	}
	rpcInMemoryRegister(n)
	return n
}

func hash(v []byte) uint64 {
	digest := udwCryptoSha3.Sum224(v)
	return binary.LittleEndian.Uint64(digest[:])
}

func (n *node) store(v []byte) {
	n.lock.Lock()
	n.keyMap[hash(v)] = v
	n.lock.Unlock()
}

func (n *node) findNode(targetId uint64) (closetId uint64) {
	return 0
}

func (n *node) findValue(key uint64) (value []byte) {
	n.lock.RLock()
	v, exist := n.keyMap[key]
	n.lock.RUnlock()
	if exist {
		return v
	}
	var min uint64 = math.MaxUint64
	var minId uint64
	for id := range n.knownNodes {
		_min := key^id
		if _min < min {
			minId = id
		}
	}
	_node := rpcInMemoryGetNode(minId)
	return _node.findValue(key)
}