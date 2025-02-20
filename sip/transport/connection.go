package transport

import (
	"iter"
	"net/netip"
	"sync"
)

type connPool struct {
	mu    sync.RWMutex
	conns connIndex
}

type connMap = map[any]struct{}

type connIndex = map[netip.AddrPort]connMap

func (p *connPool) initKey(k netip.AddrPort) {
	if p.conns == nil {
		p.conns = make(connIndex)
	}
	if p.conns[k] == nil {
		p.conns[k] = make(connMap)
	}
}

func (p *connPool) Add(k netip.AddrPort, c any) {
	p.mu.Lock()
	p.initKey(k)
	p.conns[k][c] = struct{}{}
	p.mu.Unlock()
}

func (p *connPool) Del(k netip.AddrPort, c any) {
	p.mu.Lock()
	delete(p.conns[k], c)
	if len(p.conns[k]) == 0 {
		delete(p.conns, k)
	}
	p.mu.Unlock()
}

func (p *connPool) Get(k netip.AddrPort) any {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for c := range p.conns[k] {
		return c
	}
	return nil
}

func (p *connPool) All() iter.Seq[any] {
	return func(yield func(any) bool) {
		p.mu.RLock()
		defer p.mu.RUnlock()
		for k := range p.conns {
			for c := range p.conns[k] {
				if !yield(c) {
					return
				}
			}
		}
	}
}

func (p *connPool) AllKey(k netip.AddrPort) iter.Seq[any] {
	return func(yield func(any) bool) {
		p.mu.RLock()
		defer p.mu.RUnlock()
		for c := range p.conns[k] {
			if !yield(c) {
				return
			}
		}
	}
}

func (p *connPool) Clear() {
	p.mu.Lock()
	p.conns = nil
	p.mu.Unlock()
}
