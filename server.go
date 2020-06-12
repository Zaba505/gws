package graphql_transport_ws

import (
	"context"
	"net/http"

	"nhooyr.io/websocket"
)

type option func(*websocket.AcceptOptions)

// WithSubprotocols specifies the server's supported protocols in order of
// preference. If this field is not nil, then the Upgrade method negotiates a
// subprotocol by selecting the first match in this list with a protocol
// requested by the client. If there's no match, then no protocol is
// negotiated (the Sec-Websocket-Protocol header is not included in the
// handshake response).
//
func WithSubprotocols(protocols ...string) option {
	return func(up *websocket.AcceptOptions) {
		up.Subprotocols = append(up.Subprotocols, protocols...)
	}
}

// MessageHandler is a user provided function for handling
// incoming GraphQL queries. All other "GraphQL over Websocket"
// protocol messages are automatically handled internally.
// All resolvers errors should be included in *Response and
// any validation error should be returned as error.
//
type MessageHandler func(context.Context, *Request) (*Response, error)

type handler struct {
	*websocket.AcceptOptions

	msgHandler MessageHandler
}

// NewHandler configures an http.Handler, which will upgrade
// incoming connections to websocket.
//
func NewHandler(h MessageHandler, opts ...option) http.Handler {
	up := &websocket.AcceptOptions{
		Subprotocols: []string{"graphql-ws"},
	}

	for _, opt := range opts {
		opt(up)
	}

	return &handler{AcceptOptions: up, msgHandler: h}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	wc, err := websocket.Accept(w, req, h.AcceptOptions)
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
			// TODO
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
		// TODO
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
