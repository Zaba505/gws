package graphql_transport_ws

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"nhooyr.io/websocket"
)

// Conn is a client connection that should be closed by the client.
type Conn struct {
	wc *websocket.Conn

	in, out chan operationMessage

	done chan struct{}
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

	msg := new(operationMessage)
	for {
		_, b, err := c.wc.Read(context.TODO())
		if err != nil {
			return
		}

		err = msg.UnmarshalJSON(b)
		if err != nil {
			return
		}

		select {
		case <-c.done:
			return
		case c.in <- *msg:
			break
		}
	}
}

func (c *Conn) writeMessages() {
	buf := new(bytes.Buffer)
	enc := json.NewEncoder(buf)

	for {
		select {
		case <-c.done:
			return
		case op := <-c.out:
			buf.Reset()

			err := enc.Encode(&op)
			if err != nil {
				return
			}

			err = c.wc.Write(context.TODO(), websocket.MessageBinary, buf.Bytes())
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
	return c.wc.Close(websocket.StatusNormalClosure, "closed")
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

	d := &websocket.DialOptions{
		HTTPClient:   http.DefaultClient,
		Subprotocols: []string{"graphql-ws"},
	}

	// TODO: Handle resp
	wc, _, err := websocket.Dial(ctx, endpoint, d)
	if err != nil {
		return nil, err
	}

	return newConn(wc), nil
}
