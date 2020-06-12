package graphql_transport_ws

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"nhooyr.io/websocket"
)

func TestErrMessage(t *testing.T) {
	srv := httptest.NewServer(NewHandler(testHandler))
	defer srv.Close()

	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
	if err != nil {
		t.Error(err)
		return
	}
	defer conn.Close()

	err = conn.write(context.Background(), operationMessage{Type: gql_CONNECTION_INIT})
	if err != nil {
		t.Error(err)
		return
	}

	// Should be ack message
	_, err = conn.read(context.Background())
	if err != nil {
		t.Error(err)
		return
	}

	err = conn.wc.Write(context.Background(), websocket.MessageBinary, []byte(`{"type":"start"`))
	if err != nil {
		t.Error(err)
		return
	}

	b, err := conn.read(context.Background())
	if err != nil {
		t.Error(err)
		return
	}

	msg := new(operationMessage)
	err = msg.UnmarshalJSON(b)
	if err != nil {
		t.Error(err)
		return
	}

	err, ok := msg.Payload.(error)
	if !ok {
		t.Log("expected error message from server")
		t.Fail()
		return
	}

	var serr *ServerError
	if !errors.As(err, &serr) {
		t.Log("wrong error type:", err)
		t.Fail()
		return
	}
	t.Log(serr)
}

func errHandler(ctx context.Context, req *Request) (*Response, error) {
	return nil, errors.New("test error from message handler")
}

func TestHandlerError(t *testing.T) {
	srv := httptest.NewServer(NewHandler(errHandler))
	defer srv.Close()

	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
	if err != nil {
		t.Error(err)
		return
	}
	defer conn.Close()

	err = conn.write(context.Background(), operationMessage{Type: gql_CONNECTION_INIT})
	if err != nil {
		t.Error(err)
		return
	}

	// Should be ack message
	_, err = conn.read(context.Background())
	if err != nil {
		t.Error(err)
		return
	}

	err = conn.write(context.Background(), operationMessage{
		Id:      "1",
		Type:    gql_START,
		Payload: &Request{Query: "{ hello { world } }"},
	})
	if err != nil {
		t.Error(err)
		return
	}

	b, err := conn.read(context.Background())
	if err != nil {
		t.Error(err)
		return
	}

	msg := new(operationMessage)
	err = msg.UnmarshalJSON(b)
	if err != nil {
		t.Error(err)
		return
	}

	err, ok := msg.Payload.(error)
	if !ok {
		t.Log("expected error message from server")
		t.Fail()
		return
	}

	var serr *ServerError
	if !errors.As(err, &serr) {
		t.Log("wrong error type:", err)
		t.Fail()
		return
	}
	t.Log(serr)
}
