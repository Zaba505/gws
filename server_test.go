package gws

import (
	"context"
	"errors"
	"flag"
	"net"
	"net/http"
	"net/http/httptest"
	_ "net/http/pprof"
	"strconv"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestServerOptions(t *testing.T) {
	opts := []ServerOption{
		WithOrigins("test.example.com"),
		WithCompression(CompressionDisabled, 0),
		WithMessageType(MessageText),
	}

	srv := httptest.NewServer(NewHandler(HandlerFunc(testHandler), opts...))
	defer srv.Close()

	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
	if err != nil {
		t.Error(err)
		return
	}

	conn.Close()
}

func TestServerKeepAlive(t *testing.T) {
	opts := []ServerOption{
		WithKeepAlive(500 * time.Millisecond),
	}

	srv := httptest.NewServer(NewHandler(HandlerFunc(testHandler), opts...))
	defer srv.Close()

	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
	if err != nil {
		t.Error(err)
		return
	}
	defer conn.Close()

	err = conn.write(context.Background(), operationMessage{Type: gqlConnectionInit})
	if err != nil {
		t.Error(err)
		return
	}

	// Should be the ack message
	_, err = conn.read(context.Background())
	if err != nil {
		t.Error(err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	b, err := conn.read(ctx)
	cancel()
	if err != nil {
		t.Error(err)
		return
	}

	resp := new(operationMessage)
	if err = resp.UnmarshalJSON(b); err != nil {
		t.Error(err)
		return
	}
	if resp.Type != gqlConnectionKeepAlive {
		t.Logf("expected connection keep alive but got: %s", resp.Type)
		t.Fail()
		return
	}

	ctx, cancel = context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	b, err = conn.read(ctx)
	if err != nil {
		t.Error(err)
		return
	}

	if err = resp.UnmarshalJSON(b); err != nil {
		t.Error(err)
		return
	}
	if resp.Type != gqlConnectionKeepAlive {
		t.Logf("expected connection keep alive but got: %s", resp.Type)
		t.Fail()
		return
	}
}

func TestStream_SendAfterClose(t *testing.T) {
	srv := httptest.NewServer(NewHandler(HandlerFunc(func(s *Stream, req *Request) error {
		s.Close()
		err := s.Send(context.TODO(), &Response{Data: []byte(`{"hello":{"world":"1"}}`)})
		if err == nil {
			t.Log("expected error for sending after close")
			t.Fail()
		}
		return nil
	})))
	defer srv.Close()

	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
	if err != nil {
		t.Error(err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := NewClient(conn)
	sub, err := client.Subscribe(ctx, &Request{Query: "{ hello { world } }"})
	if err != nil {
		t.Log("unexpected error", err)
		t.Fail()
		return
	}
	defer sub.Unsubscribe()

	_, err = sub.Recv(context.TODO())
	if err == nil {
		t.Log("expected error")
		t.Fail()
		return
	}
}

func TestStream_ConcurrentSend(t *testing.T) {
	srv := httptest.NewServer(NewHandler(HandlerFunc(func(s *Stream, req *Request) error {
		var wg sync.WaitGroup
		wg.Add(2)

		f := func() {
			defer wg.Done()
			err := s.Send(context.TODO(), &Response{Data: []byte(`{"hello":{"world":"1"}}`)})
			if err != nil {
				t.Log("expected error for sending after close")
				t.Fail()
			}
		}

		go f()
		go f()

		wg.Wait()

		return s.Close()
	})))
	defer srv.Close()

	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
	if err != nil {
		t.Error(err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := NewClient(conn)
	sub, err := client.Subscribe(ctx, &Request{Query: "{ hello { world } }"})
	if err != nil {
		t.Log("unexpected error", err)
		t.Fail()
		return
	}
	defer sub.Unsubscribe()

	for {
		_, err = sub.Recv(context.TODO())
		if err != nil && err != ErrUnsubscribed {
			t.Log("expected error")
			t.Fail()
			return
		}
		if err == ErrUnsubscribed {
			return
		}
	}
}

func TestStream_StopOnMessage(t *testing.T) {
	unsubscribed := make(chan struct{})
	done := make(chan struct{})

	srv := httptest.NewServer(NewHandler(HandlerFunc(func(s *Stream, req *Request) error {
		defer close(done)
		defer s.Close()

		<-unsubscribed

		for {
			err := s.Send(context.TODO(), &Response{Data: []byte(`{"hello":{"world":"1"}}`)})
			if err != nil && err != ErrStreamClosed {
				t.Error(err)
				return err
			}
			if err == ErrStreamClosed {
				return nil
			}
		}
	})))
	defer srv.Close()

	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
	if err != nil {
		t.Error(err)
		return
	}
	defer conn.Close()

	client := NewClient(conn)

	sub, err := client.Subscribe(context.TODO(), &Request{Query: "{ hello { world } }"})
	if err != nil {
		t.Error(err)
		return
	}

	err = sub.Unsubscribe()
	if err != nil {
		t.Error(err)
		return
	}

	close(unsubscribed)
	<-done
}

func TestErrMessage(t *testing.T) {
	srv := httptest.NewServer(NewHandler(HandlerFunc(testHandler)))
	defer srv.Close()

	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
	if err != nil {
		t.Error(err)
		return
	}
	defer conn.Close()

	err = conn.write(context.Background(), operationMessage{Type: gqlConnectionInit})
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

func errHandler(*Stream, *Request) error {
	return errors.New("test error from handler")
}

func TestHandlerError(t *testing.T) {
	srv := httptest.NewServer(NewHandler(HandlerFunc(errHandler)))
	defer srv.Close()

	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
	if err != nil {
		t.Error(err)
		return
	}
	defer conn.Close()

	err = conn.write(context.Background(), operationMessage{Type: gqlConnectionInit})
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
		ID:      "1",
		Type:    gqlStart,
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
	mux.Handle("/graphql", NewHandler(HandlerFunc(testHandler)))

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

func ExampleNewHandler() {
	h := func(s *Stream, req *Request) error {
		// Remember to always close the stream when done sending.
		defer s.Close()

		// Use your choice of a GraphQL runtime to execute the query
		// Then, return the results JSON encoded with a *Response.
		r := &Response{
			Data: []byte(`{"hello":{"world":"this is example data"}}`),
		}
		return s.Send(context.TODO(), r)
	}

	// Simply register the handler with a http mux of your choice
	// and it will handle the rest.
	//
	http.Handle("graphql", NewHandler(HandlerFunc(h)))

	err := http.ListenAndServe(":80", nil)
	if err != nil {
		// Always handle your errors
		return
	}
}
