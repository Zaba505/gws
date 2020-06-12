package graphql_transport_ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"nhooyr.io/websocket"
	"testing"
)

func TestWithHeaders(t *testing.T) {
	aOpts := &websocket.AcceptOptions{
		Subprotocols: []string{"graphql-ws"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, err := websocket.Accept(w, req, aOpts)
		if err != nil {
			t.Fail()
			return
		}
		m := req.Header.Get("Hello")
		if m != "World" {
			t.Fail()
		}
	}))
	defer srv.Close()

	headers := make(http.Header)
	headers.Add("Hello", "World")

	opts := []DialOption{
		WithHeaders(headers),
	}
	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String(), opts...)
	if err != nil {
		t.Error(err)
		return
	}

	conn.Close()
}
