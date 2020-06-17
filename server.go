package gws

import (
	"context"
	"errors"
	"net/http"

	"nhooyr.io/websocket"
)

// ErrStreamClosed is returned by Send if the stream
// is closed before Send is called.
//
var ErrStreamClosed = errors.New("gws: stream is closed")

// Handler is for handling incoming GraphQL queries. All other
// "GraphQL over Websocket" protocol messages are automatically
// handled internally.
//
// All resolvers errors should be included in *Response and
// any validation error should be returned as error.
//
type Handler interface {
	ServeGraphQL(*Stream, *Request) error
}

// HandlerFunc
type HandlerFunc func(*Stream, *Request) error

// ServeGraphQL implements the Handler interface.
func (f HandlerFunc) ServeGraphQL(s *Stream, req *Request) error {
	return f(s, req)
}

// Stream is used for streaming responses back to the client.
type Stream struct {
	conn *Conn
	id   opID

	done chan struct{}
}

// Send sends a response to the client. It is safe for concurrent use.
func (s *Stream) Send(ctx context.Context, resp *Response) error {
	select {
	case <-s.done:
		return ErrStreamClosed
	default:
	}

	err := s.conn.write(ctx, operationMessage{ID: s.id, Type: gqlData, Payload: resp})
	return err
}

// Close notifies the client that no more results will be sent
// and closes the stream. It also frees any resources associated
// with the stream, thus meaning Close should always be called to
// prevent any leaks.
//
func (s *Stream) Close() error {
	close(s.done)

	return s.conn.write(context.TODO(), operationMessage{ID: s.id, Type: gqlComplete})
}

type options struct {
	origins   []string
	mode      CompressionMode
	threshold int
}

// ServerOption allows the user to configure the handler.
type ServerOption interface {
	SetServer(*options)
}

type soptFn func(*options)

func (f soptFn) SetServer(opts *options) { f(opts) }

// WithOrigins lists the host patterns for authorized origins.
// The request host is always authorized. Use this to allow
// cross origin WebSockets.
//
func WithOrigins(origins ...string) ServerOption {
	return soptFn(func(opts *options) {
		opts.origins = origins
	})
}

type handler struct {
	Handler

	wcOptions *websocket.AcceptOptions
}

// NewHandler configures an http.Handler, which will upgrade
// incoming connections to WebSocket and serve the "graphql-ws" subprotocol.
//
func NewHandler(h Handler, opts ...ServerOption) http.Handler {
	sopts := &options{}

	for _, opt := range opts {
		opt.SetServer(sopts)
	}

	return &handler{
		Handler: h,
		wcOptions: &websocket.AcceptOptions{
			Subprotocols:         []string{"graphql-ws"},
			OriginPatterns:       sopts.origins,
			CompressionMode:      websocket.CompressionMode(sopts.mode),
			CompressionThreshold: sopts.threshold,
		},
	}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	wc, err := websocket.Accept(w, req, h.wcOptions)
	if err != nil {
		// TODO: Handle error
		return
	}
	conn := newConn(wc)

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	defer wc.CloseRead(ctx)

	// Handle messages
	msg := new(operationMessage)
	for {
		b, err := conn.read(ctx)
		if err != nil {
			// TODO
			return
		}

		err = msg.UnmarshalJSON(b)
		if err != nil {
			conn.write(ctx, operationMessage{
				Type:    gqlError,
				Payload: &ServerError{Msg: "received malformed message"},
			})
			continue
		}

		switch msg.Type {
		case gqlConnectionInit:
			conn.write(ctx, operationMessage{Type: gqlConnectionAck})
			break
		case gqlStart:
			cp := msg.Payload.(*Request)

			go handleRequest(conn, h, msg.ID, cp)
			break
		case gqlStop:
			// TODO: should stop be handle by the message handler
		case gqlConnectionTerminate:
			return
		default:
			// TODO: Handle
			break
		}
	}
}

func handleRequest(conn *Conn, h Handler, id opID, req *Request) {
	s := &Stream{
		id:   id,
		conn: conn,
		done: make(chan struct{}, 1),
	}

	err := h.ServeGraphQL(s, req)
	if err != nil {
		conn.write(context.TODO(), operationMessage{
			ID:      id,
			Type:    gqlError,
			Payload: &ServerError{Msg: err.Error()},
		})
		return
	}
}
