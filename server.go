package graphql_transport_ws

import (
	"context"
	"net/http"

	"nhooyr.io/websocket"
)

// MessageHandler is a user provided function for handling
// incoming GraphQL queries. All other "GraphQL over Websocket"
// protocol messages are automatically handled internally.
// All resolvers errors should be included in *Response and
// any validation error should be returned as error.
//
type MessageHandler func(context.Context, *Request) (*Response, error)

type options struct {
	origins   []string
	mode      websocket.CompressionMode
	threshold int
}

// ServerOption allows the user to configure the handler.
type ServerOption func(*options)

// WithOrigins lists the host patterns for authorized origins.
// The request host is always authorized. Use this to allow
// cross origin WebSockets.
//
func WithOrigins(origins ...string) ServerOption {
	return func(opts *options) {
		opts.origins = origins
	}
}

type handler struct {
	wcOptions *websocket.AcceptOptions

	msgHandler MessageHandler
}

// NewHandler configures an http.Handler, which will upgrade
// incoming connections to WebSocket and serve the "graphql-ws" subprotocol.
//
func NewHandler(h MessageHandler, opts ...ServerOption) http.Handler {
	sopts := &options{}

	for _, opt := range opts {
		opt(sopts)
	}

	return &handler{
		wcOptions: &websocket.AcceptOptions{
			Subprotocols:         []string{"graphql-ws"},
			OriginPatterns:       sopts.origins,
			CompressionMode:      sopts.mode,
			CompressionThreshold: sopts.threshold,
		},
		msgHandler: h,
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
				Type:    gql_ERROR,
				Payload: &ServerError{Msg: "received malformed message"},
			})
			continue
		}

		switch msg.Type {
		case gql_CONNECTION_INIT:
			conn.write(ctx, operationMessage{Type: gql_CONNECTION_ACK})
			break
		case gql_START:
			cp := msg.Payload.(*Request)

			go handleRequest(ctx, conn, h.msgHandler, msg.Id, cp)
			break
		case gql_STOP:
			// TODO: should stop be handle by the message handler
		case gql_CONNECTION_TERMINATE:
			return
		default:
			// TODO: Handle
			break
		}
	}
}

func handleRequest(ctx context.Context, conn *Conn, h MessageHandler, id opId, req *Request) {
	resp, err := h(ctx, req)
	if err != nil {
		conn.write(ctx, operationMessage{
			Id:      id,
			Type:    gql_ERROR,
			Payload: &ServerError{Msg: err.Error()},
		})
		return
	}

	msg := operationMessage{
		Id:      id,
		Type:    gql_DATA,
		Payload: resp,
	}

	err = conn.write(ctx, msg)
	if err != nil {
		// TODO: Handle error
		return
	}

	err = conn.write(ctx, operationMessage{Id: id, Type: gql_COMPLETE})
	if err != nil {
		// TODO: Handle error
		return
	}
}
