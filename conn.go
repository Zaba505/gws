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
	client      *http.Client
	headers     http.Header
	compression CompressionMode
	threshold   int
}

// DialOption
type DialOption interface {
	// Set
	Set(*dialOpts)
}

type optionFn func(*dialOpts)

func (f optionFn) Set(opts *dialOpts) { f(opts) }

// CompressionMode represents the modes available to the deflate extension. See
// https://tools.ietf.org/html/rfc7692
//
// A compatibility layer is implemented for the older deflate-frame extension
// used by safari. See
// https://tools.ietf.org/html/draft-tyoshino-hybi-websocket-perframe-deflate-06
// It will work the same in every way except that we cannot signal to the peer
// we want to use no context takeover on our side, we can only signal that they
// should. It is however currently disabled due to Safari bugs. See
// https://github.com/nhooyr/websocket/issues/218
//
type CompressionMode websocket.CompressionMode

const (
	// CompressionNoContextTakeover grabs a new flate.Reader and flate.Writer as needed
	// for every message. This applies to both server and client side.
	//
	// This means less efficient compression as the sliding window from previous messages
	// will not be used but the memory overhead will be lower if the connections
	// are long lived and seldom used.
	//
	// The message will only be compressed if greater than 512 bytes.
	//
	CompressionNoContextTakeover CompressionMode = iota

	// CompressionContextTakeover uses a flate.Reader and flate.Writer per connection.
	// This enables reusing the sliding window from previous messages.
	// As most WebSocket protocols are repetitive, this can be very efficient.
	// It carries an overhead of 8 kB for every connection compared to CompressionNoContextTakeover.
	//
	// If the peer negotiates NoContextTakeover on the client or server side, it will be
	// used instead as this is required by the RFC.
	//
	CompressionContextTakeover

	// CompressionDisabled disables the deflate extension.
	//
	// Use this if you are using a predominantly binary protocol with very
	// little duplication in between messages or CPU and memory are more
	// important than bandwidth.
	//
	CompressionDisabled
)

// WithCompression configures compression over the WebSocket.
// By default, compression is disabled and for now is considered
// an experimental feature.
//
func WithCompression(mode CompressionMode, threshold int) DialOption {
	return optionFn(func(opts *dialOpts) {
		opts.compression = mode
		opts.threshold = threshold
	})
}

// WithHTTPClient provides an http.Client to override the default one used.
func WithHTTPClient(client *http.Client) DialOption {
	return optionFn(func(opts *dialOpts) {
		opts.client = client
	})
}

// WithHeaders adds custom headers to every dial HTTP request.
func WithHeaders(headers http.Header) DialOption {
	return optionFn(func(opts *dialOpts) {
		opts.headers = headers
	})
}

// Dial
func Dial(ctx context.Context, endpoint string, opts ...DialOption) (*Conn, error) {
	dopts := &dialOpts{
		client: http.DefaultClient,
	}

	for _, opt := range opts {
		opt.Set(dopts)
	}

	d := &websocket.DialOptions{
		HTTPClient:           dopts.client,
		HTTPHeader:           dopts.headers,
		Subprotocols:         []string{"graphql-ws"},
		CompressionMode:      websocket.CompressionMode(dopts.compression),
		CompressionThreshold: dopts.threshold,
	}

	// TODO: Handle resp
	wc, _, err := websocket.Dial(ctx, endpoint, d)
	if err != nil {
		return nil, err
	}

	return newConn(wc), nil
}
