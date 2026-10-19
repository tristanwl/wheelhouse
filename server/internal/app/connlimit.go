package app

import (
	"net"
	"sync"
)

// MaxConnections is the most connections the server holds open at once. The
// default descriptor limit of a service manager is 1024, and the database, the
// ffmpeg processes and the upstream clients need descriptors too, so a flood of
// idle or half-open connections must not be able to use them all. An accepted
// connection past the limit waits in the kernel's backlog until one closes.
// There is no streaming or long polling in the API (the conversion progress is
// an ordinary GET the page repeats), so no legitimate client holds a
// connection for long.
const MaxConnections = 512

// limitListener returns ln limited to n open connections.
func limitListener(ln net.Listener, n int) net.Listener {
	return &limited{Listener: ln, slots: make(chan struct{}, n), done: make(chan struct{})}
}

type limited struct {
	net.Listener
	slots chan struct{}
	once  sync.Once
	done  chan struct{}
}

func (l *limited) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &limitedConn{Conn: c, release: func() { <-l.slots }}, nil
}

func (l *limited) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.Listener.Close()
}

type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
