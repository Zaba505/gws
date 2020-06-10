package graphql_transport_ws

import (
	"context"
	"net/http"

	"github.com/gorilla/websocket"
)

type option func(*websocket.Upgrader)

// WithCheckOrigin returns true if the request Origin header is acceptable. If
// CheckOrigin is nil, then a safe default is used: return false if the
// Origin request header is present and the origin host is not equal to
// request Host header.
//
// A CheckOrigin function should carefully validate the request origin to
// prevent cross-site request forgery.
//
func WithCheckOrigin(f func(r *http.Request) bool) option {
	return func(up *websocket.Upgrader) {
		up.CheckOrigin = f
	}
}

// WithSubprotocols specifies the server's supported protocols in order of
// preference. If this field is not nil, then the Upgrade method negotiates a
// subprotocol by selecting the first match in this list with a protocol
// requested by the client. If there's no match, then no protocol is
// negotiated (the Sec-Websocket-Protocol header is not included in the
// handshake response).
//
func WithSubprotocols(protocols ...string) option {
	return func(up *websocket.Upgrader) {
		up.Subprotocols = append(up.Subprotocols, protocols...)
	}
}

// MessageHandler is a user provided function for handling
// incoming GraphQL queries. All other "GraphQL over Websocket"
// protocol messages are automatically handled internally.
//
type MessageHandler func(context.Context, *Request) (*Response, error)

type handler struct {
	*websocket.Upgrader

	msgHandler MessageHandler
}

// NewHandler configures an http.Handler, which will upgrade
// incoming connections to websocket.
//
func NewHandler(h MessageHandler, opts ...option) http.Handler {
	up := &websocket.Upgrader{
		Subprotocols: []string{"graphql-ws"},
	}

	for _, opt := range opts {
		opt(up)
	}

	return &handler{Upgrader: up, msgHandler: h}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	wc, err := h.Upgrade(w, req, req.Header)
	if err != nil {
		// TODO: Handle error
		return
	}
	conn := newConn(wc)
	defer conn.Close()

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	// Handle messages
	for msg := range conn.in {
		switch msg.Type {
		case gql_CONNECTION_INIT:
			conn.send(ctx, operationMessage{Type: gql_CONNECTION_ACK})
			break
		case gql_START:
			cp := msg.Payload.(*Request)

			go handleRequest(ctx, h.msgHandler, msg.Id, cp, conn.out)
			break
		case gql_CONNECTION_TERMINATE:
			cancel()
			return
		default:
			// TODO: Handle
			break
		}
	}
}

func handleRequest(ctx context.Context, h MessageHandler, id opId, req *Request, ops chan<- operationMessage) {
	resp, err := h(ctx, req)
	if err != nil {
		// TODO
	}

	msg := operationMessage{
		Id:      id,
		Type:    gql_DATA,
		Payload: resp,
	}

	select {
	case <-ctx.Done():
		return
	case ops <- msg:
		return
	}
}
