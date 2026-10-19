package app

import (
	"net"
	"testing"
	"time"
)

func TestLimitListenerHoldsBackConnectionsPastTheLimitUntilOneCloses(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := limitListener(raw, 2)
	defer func() { _ = ln.Close() }()

	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	dial := func() net.Conn {
		c, err := net.Dial("tcp", raw.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	dial()
	dial()
	dial()
	first, second := <-accepted, <-accepted
	select {
	case <-accepted:
		t.Fatal("a third connection was accepted past the limit of 2")
	case <-time.After(150 * time.Millisecond):
	}
	_ = first.Close()
	_ = first.Close() // closing twice must not free two slots
	select {
	case c := <-accepted:
		_ = c.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("the waiting connection was not accepted after a slot freed")
	}
	_ = second.Close()
}
