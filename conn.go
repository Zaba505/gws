package graphql_transport_ws

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Conn is a client connection that should be closed by the client.
type Conn struct {
	wc *websocket.Conn

	in, out chan operationMessage

	done chan struct{}

	msgId uint64
	msgs  chan operationMessage

	msgMu   sync.Mutex
	msgSubs map[opId]chan<- *Response
}

func newConn(wc *websocket.Conn) *Conn {
	c := &Conn{
		wc:   wc,
		in:   make(chan operationMessage),
		out:  make(chan operationMessage),
		done: make(chan struct{}, 1),
	}

	go c.readMessages()
	go c.writeMessages()

	return c
}

func (c *Conn) readMessages() {
	defer close(c.in)

	for {
		var msg operationMessage
		err := c.wc.ReadJSON(&msg)
		if err != nil {
			return
		}

		select {
		case <-c.done:
			return
		case c.in <- msg:
			break
		}
	}
}

func (c *Conn) writeMessages() {
	for {
		select {
		case <-c.done:
			return
		case op := <-c.out:
			err := c.wc.WriteJSON(&op)
			if err != nil {
				return
			}
		}
	}
}

func (c *Conn) send(ctx context.Context, msg operationMessage) {
	select {
	case <-c.done:
		return
	case <-ctx.Done():
		return
	case c.out <- msg:
		return
	}
}

// Close closes the underlying WebSocket connection.
func (c *Conn) Close() error {
	close(c.done)
	return c.wc.Close()
}

type dialOpts struct {
	headers http.Header
}

// DialOption
type DialOption interface {
	// Set
	Set(*dialOpts)
}

type optionFn func(*dialOpts)

func (f optionFn) Set(opts *dialOpts) { f(opts) }

// WithHeaders
func WithHeaders(headers http.Header) DialOption {
	return optionFn(func(opts *dialOpts) {
		opts.headers = headers
	})
}

// Dial
func Dial(ctx context.Context, endpoint string, opts ...DialOption) (*Conn, error) {
	dopts := new(dialOpts)
	for _, opt := range opts {
		opt.Set(dopts)
	}

	d := &websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
		Subprotocols:     []string{"graphql-ws"},
	}

	// TODO: Handle resp
	wc, _, err := d.DialContext(ctx, endpoint, dopts.headers)
	if err != nil {
		return nil, err
	}

	return newConn(wc), nil
}
