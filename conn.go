package gws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/zaba505/gws/backoff"
	internalbackoff "github.com/zaba505/gws/internal/backoff"

	"nhooyr.io/websocket"
)

const minConnectTimeout = 20 * time.Second

type dialOpts struct {
	bs                internalbackoff.Strategy
	minConnectTimeout func() time.Duration
	client            *http.Client
	headers           http.Header
	compression       CompressionMode
	threshold         int
	typ               MessageType
}

// DialOption configures how we set up the connection.
type DialOption interface {
	SetDial(*dialOpts)
}

type optionFn func(*dialOpts)

func (f optionFn) SetDial(opts *dialOpts) { f(opts) }

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

// ConnOption represents a configuration that applies symmetrically
// on both sides, client and server.
//
type ConnOption interface {
	DialOption
	ServerOption
}

type compression struct {
	mode      CompressionMode
	threshold int
}

func (opt compression) SetDial(opts *dialOpts) {
	opts.compression = opt.mode
	opts.threshold = opt.threshold
}

func (opt compression) SetServer(opts *options) {
	opts.mode = opt.mode
	opts.threshold = opt.threshold
}

// WithCompression configures compression over the WebSocket.
// By default, compression is disabled and for now is considered
// an experimental feature.
//
func WithCompression(mode CompressionMode, threshold int) ConnOption {
	return compression{
		mode:      mode,
		threshold: threshold,
	}
}

// MessageType represents the type of a Websocket message.
type MessageType websocket.MessageType

// Re-export message type provided by the underlying Websocket package.
const (
	MessageText   = MessageType(websocket.MessageText)
	MessageBinary = MessageType(websocket.MessageBinary)
)

type mtyp MessageType

func (t mtyp) SetDial(opts *dialOpts) {
	opts.typ = MessageType(t)
}

func (t mtyp) SetServer(opts *options) {
	opts.typ = MessageType(t)
}

// WithMessageType allows users to set the underlying WebSocket message encoding.
// Default is MessageBinary.
//
// Note: for browser clients like Apollo GraphQL the MessageText encoding should be used.
//
func WithMessageType(typ MessageType) ConnOption {
	return mtyp(typ)
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

// ConnectParams defines the parameters for connecting and retrying. Users are
// encouraged to use this instead of the BackoffConfig type defined above. See
// here for more details:
// https://github.com/grpc/grpc/blob/master/doc/connection-backoff.md.
//
// This API is EXPERIMENTAL.
type ConnectParams struct {
	// Backoff specifies the configuration options for connection backoff.
	Backoff backoff.Config
	// MinConnectTimeout is the minimum amount of time we are willing to give a
	// connection to complete.
	MinConnectTimeout time.Duration
}

// DefaultConnectParams is a default configuration for retrying with a backoff.
var DefaultConnectParams = ConnectParams{
	Backoff:           backoff.DefaultConfig,
	MinConnectTimeout: 20 * time.Second,
}

// WithConnectParams configures the client to use the provided ConnectParams.
func WithConnectParams(p ConnectParams) DialOption {
	return optionFn(func(opts *dialOpts) {
		opts.bs = internalbackoff.Exponential{Config: p.Backoff}
		opts.minConnectTimeout = func() time.Duration {
			return p.MinConnectTimeout
		}
	})
}

// Conn is a client connection that should be closed by the client.
type Conn struct {
	mtyp    websocket.MessageType
	wc      *websocket.Conn
	bufPool *sync.Pool

	done chan struct{}
}

func newConn(wc *websocket.Conn, typ MessageType) *Conn {
	c := &Conn{
		mtyp: websocket.MessageType(typ),
		wc:   wc,
		bufPool: &sync.Pool{
			New: func() interface{} {
				return new(bytes.Buffer)
			},
		},
		done: make(chan struct{}, 1),
	}

	return c
}

// Dial creates a connection to the given endpoint. By default, it's a non-blocking
// dial (the function won't wait for connections to be established, and connecting
// happens in the background).
//
func Dial(ctx context.Context, endpoint string, opts ...DialOption) (*Conn, error) {
	fopts := []DialOption{
		WithHTTPClient(http.DefaultClient),
		WithMessageType(MessageBinary),
		WithConnectParams(DefaultConnectParams),
	}
	fopts = append(fopts, opts...)

	dopts := new(dialOpts)
	for _, opt := range fopts {
		opt.SetDial(dopts)
	}

	// TODO: Handle resp
	wc, _, err := dial(ctx, endpoint, dopts)
	if err != nil {
		return nil, err
	}

	return newConn(wc, dopts.typ), nil
}

func dial(ctx context.Context, endpoint string, dopts *dialOpts) (wc *websocket.Conn, resp *http.Response, err error) {
	opts := &websocket.DialOptions{
		HTTPClient:           dopts.client,
		HTTPHeader:           dopts.headers,
		Subprotocols:         []string{"graphql-ws"},
		CompressionMode:      websocket.CompressionMode(dopts.compression),
		CompressionThreshold: dopts.threshold,
	}

	backoffIdx := 0
	for {
		dialDuration := dopts.minConnectTimeout()

		backoffFor := dopts.bs.Backoff(backoffIdx) // TODO count backoff
		if dialDuration < backoffFor {
			dialDuration = backoffFor
		}

		dctx, cancel := context.WithTimeout(ctx, dialDuration)
		wc, resp, err = websocket.Dial(dctx, endpoint, opts)
		cancel()
		if err == nil {
			return
		}
		var ne net.Error
		if !errors.As(err, &ne) || (!ne.Timeout() && !ne.Temporary()) {
			return
		}

		timer := time.NewTimer(backoffFor)
		select {
		case <-timer.C:
			backoffIdx++
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
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

	return c.wc.Write(ctx, c.mtyp, buf.Bytes())
}

// Close closes the underlying WebSocket connection.
func (c *Conn) Close() error {
	close(c.done)

	err := c.write(context.Background(), operationMessage{Type: gqlConnectionTerminate})
	if err != nil {
		return err
	}

	return c.wc.Close(websocket.StatusNormalClosure, "closed")
}
