package graphql_transport_ws

import (
	"encoding/json"
	"fmt"
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

// Request represents a payload sent from the client.
type Request struct {
	Query         string                 `json:"query"`
	Variables     map[string]interface{} `json:"variables"`
	OperationName string                 `json:"operationName"`
}

// Response represents a payload returned from the server. It supports
// lazy decoding by leaving the inner data for the user to decode.
//
type Response struct {
	Data   json.RawMessage   `json:"data"`
	Errors []json.RawMessage `json:"errors"`
}

// ServerError represents a payload which is sent by the server if
// it encounters a non-GraphQL resolver error.
//
type ServerError struct {
	Msg string `json:"msg"`
}

// Error implements the error interface.
func (e *ServerError) Error() string {
	return fmt.Sprintf("internal server error: %s", e.Msg)
}

// payload represents either a Client or Server payload
type payload interface {
	isPayload()
}

func (*Request) isPayload()     {}
func (*Response) isPayload()    {}
func (*ServerError) isPayload() {}

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
	var raw struct {
		Id      opId            `json:"id,omitempty"`
		Type    reqType         `json:"type"`
		Payload json.RawMessage `json:"payload,omitempty"`
	}
	err := json.Unmarshal(b, &raw)
	if err != nil {
		return err
	}

	m.Type = raw.Type
	if raw.Id != "" {
		m.Id = raw.Id
	}

	if len(raw.Payload) == 0 {
		return nil
	}

	switch raw.Type {
	case gql_CONNECTION_INIT, gql_START, gql_STOP, gql_CONNECTION_TERMINATE:
		req := new(Request)
		m.Payload = req
		return json.Unmarshal(raw.Payload, req)
	case gql_CONNECTION_ERROR, gql_CONNECTION_ACK, gql_DATA, gql_COMPLETE, gql_CONNECTION_KEEP_ALIVE:
		resp := new(Response)
		m.Payload = resp
		return json.Unmarshal(raw.Payload, resp)
	case gql_ERROR:
		serr := new(ServerError)
		m.Payload = serr
		return json.Unmarshal(raw.Payload, serr)
	default:
		return fmt.Errorf("unsupported message type: %s", raw.Type)
	}
}
