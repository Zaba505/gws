package graphql_transport_ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"nhooyr.io/websocket"
	"testing"
)

func TestWithDialOptions(t *testing.T) {
	aOpts := &websocket.AcceptOptions{
		Subprotocols: []string{"graphql-ws"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		wc, err := websocket.Accept(w, req, aOpts)
		if err != nil {
			t.Fail()
			return
		}
		wc.CloseRead(context.Background())
		m := req.Header.Get("Hello")
		if m != "World" {
			t.Fail()
		}
	}))
	defer srv.Close()

	headers := make(http.Header)
	headers.Add("Hello", "World")

	opts := []DialOption{
		WithHTTPClient(http.DefaultClient),
		WithHeaders(headers),
		WithCompression(CompressionDisabled, 0),
	}
	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String(), opts...)
	if err != nil {
		t.Error(err)
		return
	}

	conn.Close()
}

func TestTerminate(t *testing.T) {
	srv := newTestServer(func(conn *Conn) {
		defer conn.wc.CloseRead(context.Background())

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

		if msg.Type != gql_CONNECTION_TERMINATE {
			t.Log("wrong message:", msg)
			t.Fail()
			return
		}
	})
	defer srv.Close()

	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
	if err != nil {
		t.Error(err)
		return
	}
	conn.Close()
}

func ExampleDial() {
	conn, err := Dial(context.TODO(), "ws://example.com")
	if err != nil {
		// Make sure to handle the error
		return
	}
	defer conn.Close()

	// Create a single client with the connection.
	// There is no need to create multiple connections or clients
	// because it will all be managed for you.
}
