package graphql_transport_ws

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Conn is a client connection that should be closed by the client.
type Conn struct {
	wc   *websocket.Conn
	done chan struct{}

	msgId uint64
	msgs  chan operationMessage

	msgMu   sync.Mutex
	msgSubs map[opId]chan<- *Response
}

// Close closes the underlying WebSocket connection.
func (c *Conn) Close() error {
	close(c.done)
	// TODO: Send terminate message
	return nil
}

func (c *Conn) init() {
	go c.readMessages(c.done)
	// TODO: Spin up single reader goroutine
	// TODO: Send init message
	// TODO: Block any queries until ack message is recieved
	go writeMessages(c.wc, c.msgs)
}

func (c *Conn) write(ctx context.Context, msg *Request) <-chan *Response {
	respChan := make(chan *Response, 1)

	id := atomic.AddUint64(&c.msgId, 1)
	oid := opId(strconv.FormatUint(id, 10))

	go func() {
		c.msgs <- operationMessage{
			Id:      oid,
			Type:    gql_DATA,
			Payload: msg,
		}
	}()

	c.msgMu.Lock()
	c.msgSubs[oid] = respChan
	c.msgMu.Unlock()

	return respChan
}

func (c *Conn) readMessages(done <-chan struct{}) {
	for {
		opMsg := new(operationMessage)
		err := c.wc.ReadJSON(opMsg)
		if err != nil {
			// TODO: Handle error
			return
		}

		c.msgMu.Lock()
		sub := c.msgSubs[opMsg.Id]
		c.msgMu.Unlock()

		if sub == nil {
			// TODO: Handle removed sub
			return
		}
	}
}

func writeMessages(conn *websocket.Conn, msgs <-chan operationMessage) {

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

	c := &Conn{
		wc:      wc,
		done:    make(chan struct{}, 1),
		msgs:    make(chan operationMessage, 1),
		msgSubs: make(map[opId]chan<- *Response),
	}
	go c.init()

	return c, err
}
