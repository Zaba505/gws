package graphql_transport_ws

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"nhooyr.io/websocket"
)

// Conn is a client connection that should be closed by the client.
type Conn struct {
	wc      *websocket.Conn
	bufPool *sync.Pool

	done chan struct{}
}

func newConn(wc *websocket.Conn) *Conn {
	c := &Conn{
		wc: wc,
		bufPool: &sync.Pool{
			New: func() interface{} {
				return new(bytes.Buffer)
			},
		},
		done: make(chan struct{}, 1),
	}

	return c
}

func (c *Conn) read(ctx context.Context) ([]byte, error) {
	_, b, err := c.wc.Read(ctx)
	return b, err
}

func (c *Conn) write(ctx context.Context, msg operationMessage) error {
	buf := c.bufPool.Get().(*bytes.Buffer)
	defer func() {
		buf.Reset()
		c.bufPool.Put(buf)
	}()

	enc := json.NewEncoder(buf)

	err := enc.Encode(&msg)
	if err != nil {
		return err
	}

	return c.wc.Write(ctx, websocket.MessageBinary, buf.Bytes())
}

// Close closes the underlying WebSocket connection.
func (c *Conn) Close() error {
	close(c.done)

	err := c.write(context.Background(), operationMessage{Type: gql_CONNECTION_TERMINATE})
	if err != nil {
		return err
	}

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
		HTTPHeader:   dopts.headers,
		Subprotocols: []string{"graphql-ws"},
	}

	// TODO: Handle resp
	wc, _, err := websocket.Dial(ctx, endpoint, d)
	if err != nil {
		return nil, err
	}

	return newConn(wc), nil
}
