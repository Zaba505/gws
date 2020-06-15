package graphql_transport_ws

import (
	"context"
	"errors"
	"flag"
	"net"
	"net/http"
	"net/http/httptest"
	_ "net/http/pprof"
	"strconv"
	"testing"

	"nhooyr.io/websocket"
)

func TestServerOptions(t *testing.T) {
	opts := []ServerOption{
		WithOrigins("test.example.com"),
		WithCompression(CompressionDisabled, 0),
	}

	srv := httptest.NewServer(NewHandler(testHandler, opts...))
	defer srv.Close()

	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
	if err != nil {
		t.Error(err)
		return
	}

	conn.Close()
}

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

var (
	loadTest = flag.Bool("load", false, "Run server load test")
	port     = flag.Uint("port", 4200, "Specify local port for server to listen on")
)

func TestServerLoad(t *testing.T) {
	if !*loadTest {
		t.Skip("use artillery to load test server implementation")
		return
	}

	mux := http.DefaultServeMux
	mux.Handle("/graphql", NewHandler(func(ctx context.Context, req *Request) (*Response, error) {
		return testHandler(ctx, req)
	}))

	srv := &http.Server{
		Handler: mux,
	}

	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.FormatUint(uint64(*port), 10))
	if err != nil {
		t.Error(err)
		return
	}

	err = srv.Serve(l)
	if err != nil {
		t.Error(err)
		return
	}
}

func ExampleHandler() {
	h := func(ctx context.Context, req *Request) (*Response, error) {
		// Should observe ctx in case it gets cancelled.

		// Use your choice of a GraphQL runtime to execute the query
		// Then, return the results JSON encoded with a *Response.
		return &Response{Data: []byte(`{"hello":{"world":"this is example data"}}`)}, nil
	}

	// Simply register the handler with a http mux of your choice
	// and it will handle the rest.
	//
	http.Handle("graphql", NewHandler(MessageHandler(h)))

	err := http.ListenAndServe(":80", nil)
	if err != nil {
		// Always handle your errors
		return
	}
}
