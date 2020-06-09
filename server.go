package graphql_transport_ws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"
)

type reqType string

const (
	// Client -> Server
	gql_CONNECTION_INIT      reqType = "connection_init"
	gql_START                        = "start"
	gql_STOP                         = "stop"
	gql_CONNECTION_TERMINATE         = "connection_terminate"

	// Server -> Client
	gql_CONNECTION_ERROR      = "connection_error"
	gql_CONNECTION_ACK        = "connection_ack"
	gql_DATA                  = "data"
	gql_ERROR                 = "error"
	gql_COMPLETE              = "complete"
	gql_CONNECTION_KEEP_ALIVE = "connection_keep_alive"
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
		up.Subprotocols = protocols
	}
}

// MessageHandler is a user provided function for handling
// incoming GraphQL queries. All other "GraphQL over Websocket"
// protocol messages are automatically handled internally.
//
type MessageHandler func(context.Context, *Request) (*Response, error)

// NewHandler configures an http.Handler, which will upgrade
// incoming connections to websocket.
//
func NewHandler(h MessageHandler, opts ...option) http.Handler {
	return nil
}

// Request represents payload sent from the client
type Request struct {
	Query         string                 `json:"query"`
	Variables     map[string]interface{} `json:"variables"`
	OperationName string                 `json:"operationName"`
}

// Response represents a payload returned from the server
type Response struct {
	Data   interface{}   `json:"data"`
	Errors []interface{} `json:"errors"`
}

// Payload represents either a Client or Server payload
type payload interface {
	isPayload()
}

func (*Request) isPayload()  {}
func (*Response) isPayload() {}

type unknown map[string]interface{}

func (unknown) isPayload() {}

// opId represents a unique id per user request
type opId string

// operationMessage represents an Apollo "GraphQL over WebSockets Protocol" message
type operationMessage struct {
	Id      opId    `json:"id,omitempty"`
	Type    reqType `json:"type"`
	Payload payload `json:"payload,omitempty"`
}

func (m *operationMessage) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New("expected object opening token")
	}

	for dec.More() {
		tok, err = dec.Token()
		if err != nil {
			return err
		}

		s := tok.(string)
		switch s {
		case "id":
			tok, err = dec.Token()
			if err != nil {
				return err
			}
			m.Id = opId(tok.(string))
		case "type":
			tok, err = dec.Token()
			if err != nil {
				return err
			}
			m.Type = reqType(tok.(string))
		case "payload":
			m.Payload, err = decodePayload(m.Type, dec)
			if err != nil {
				return err
			}
		}
	}

	x, ok := m.Payload.(unknown)
	if !ok {
		return nil
	}

	mp := map[string]interface{}(x)

	switch m.Type {
	case gql_CONNECTION_INIT, gql_START, gql_STOP, gql_CONNECTION_TERMINATE:
		req := new(Request)
		m.Payload = req

		req.Query = mp["query"].(string)
		if opName, ok := mp["operationName"]; ok {
			req.OperationName = opName.(string)
		}
		if vars, ok := mp["variables"]; ok {
			req.Variables = vars.(map[string]interface{})
		}
	case gql_CONNECTION_ERROR, gql_CONNECTION_ACK, gql_DATA, gql_ERROR, gql_COMPLETE, gql_CONNECTION_KEEP_ALIVE:
		resp := new(Response)
		m.Payload = resp

		resp.Data = mp["data"]
		resp.Errors = mp["errors"].([]interface{})
	default:
		err = fmt.Errorf("unknown client type: %s", m.Type)
	}

	return err
}

func decodePayload(typ reqType, dec *json.Decoder) (payload, error) {
	switch typ {
	case gql_CONNECTION_INIT, gql_START, gql_STOP, gql_CONNECTION_TERMINATE:
		p := new(Request)

		return p, dec.Decode(p)
	case gql_CONNECTION_ERROR, gql_CONNECTION_ACK, gql_DATA, gql_ERROR, gql_COMPLETE, gql_CONNECTION_KEEP_ALIVE:
		p := new(Response)

		return p, dec.Decode(p)
	case "":
		m := make(unknown)
		return m, dec.Decode(&m)
	default:
		return nil, fmt.Errorf("unknown client type: %s", typ)
	}
}
