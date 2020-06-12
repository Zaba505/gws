package graphql_transport_ws

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func newTestServer(f func(*Conn)) *httptest.Server {
	opts := &websocket.AcceptOptions{
		Subprotocols: []string{"graphql-ws"},
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, _ := websocket.Accept(w, req, opts)
		f(newConn(conn))
	}))
}

func TestHandleServerError(t *testing.T) {
	srv := httptest.NewServer(NewHandler(errHandler))
	defer srv.Close()

	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
	if err != nil {
		t.Error(err)
		return
	}
	defer conn.Close()

	client := NewClient(conn)
	_, err = client.Query(context.Background(), &Request{Query: "{ hello { world } }"})
	if err == nil {
		t.Log("expected an error")
		t.Fail()
		return
	}

	var serr *ServerError
	if !errors.As(err, &serr) {
		t.Log("wrong err type:", err)
		t.Fail()
		return
	}
	t.Log(serr)
}

func TestFailedIO(t *testing.T) {
	srv := newTestServer(func(conn *Conn) {
		conn.wc.CloseRead(context.Background())
	})
	defer srv.Close()

	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
	if err != nil {
		t.Error(err)
		return
	}
	conn.Close()

	client := NewClient(conn)

	_, err = client.Query(context.Background(), &Request{})

	var ioErr ErrIO
	if !errors.As(err, &ioErr) {
		t.Logf("wrong error: %s", err)
		t.Fail()
		return
	}
	t.Log(ioErr)
}

func TestUnexpectedAckMessage(t *testing.T) {
	srv := newTestServer(func(conn *Conn) {
		conn.wc.CloseRead(context.Background())

		conn.write(context.Background(), operationMessage{Type: gql_DATA})
	})
	defer srv.Close()

	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
	if err != nil {
		t.Error(err)
		return
	}

	client := NewClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = client.Query(ctx, &Request{})
	if err == nil {
		t.Log("expected error")
		t.Fail()
		return
	}

	var msgErr ErrUnexpectedMessage
	if !errors.As(err, &msgErr) {
		t.Logf("wrong error: %s", err)
		t.Fail()
		return
	}
	t.Log(msgErr)
}

const (
	badAckMsg  = `{"type":"connection_ack"`
	badDataMsg = `{"type":"data","payload"}`
)

func TestMalformedMessage(t *testing.T) {
	testCases := []struct {
		Name    string
		Handler func(*Conn)
	}{
		{
			Name: "AckResponse",
			Handler: func(conn *Conn) {
				conn.wc.CloseRead(context.Background())

				conn.wc.Write(context.Background(), websocket.MessageBinary, []byte(badAckMsg))
			},
		},
		{
			Name: "DataResponse",
			Handler: func(conn *Conn) {
				conn.wc.CloseRead(context.Background())

				conn.write(context.Background(), operationMessage{Type: gql_CONNECTION_ACK})
				conn.wc.Write(context.Background(), websocket.MessageBinary, []byte(badDataMsg))
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(subT *testing.T) {
			srv := newTestServer(testCase.Handler)
			defer srv.Close()

			conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
			if err != nil {
				subT.Error(err)
				return
			}

			client := NewClient(conn)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			_, err = client.Query(ctx, &Request{})
			if err == nil {
				subT.Log("expected error")
				subT.Fail()
				return
			}

			var typErr *json.UnmarshalTypeError
			var synErr *json.SyntaxError
			if !errors.As(err, &typErr) && !errors.As(err, &synErr) {
				subT.Logf("wrong error: %s", err)
				subT.Fail()
				return
			}
		})
	}
}
