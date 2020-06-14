package graphql_transport_ws

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func testHandler(ctx context.Context, req *Request) (*Response, error) {
	return &Response{Data: []byte(`{"hello":{"world":"this is a test"}}`)}, nil
}

func TestE2E(t *testing.T) {
	srv := httptest.NewServer(NewHandler(testHandler))
	defer srv.Close()

	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
	if err != nil {
		t.Errorf("unexpected error when dialing: %s", err)
		return
	}
	defer conn.Close()

	client := NewClient(conn)
	resp, err := client.Query(context.Background(), &Request{Query: "{ hello { world } }"})
	if err != nil {
		t.Errorf("unexpected error when querying: %s", err)
		return
	}

	var testResp struct {
		Hello struct {
			World string
		}
	}
	err = json.Unmarshal(resp.Data, &testResp)
	if err != nil {
		t.Logf("response data: %s", string(resp.Data))
		t.Errorf("unexpected error when unmarshalling response: %s", err)
		return
	}

	if testResp.Hello.World != "this is a test" {
		t.Logf("expected: %s, but got: %s", "this is a test", testResp.Hello.World)
		t.Fail()
		return
	}
}

func TestConcurrency(t *testing.T) {
	srv := httptest.NewServer(NewHandler(testHandler))
	defer srv.Close()

	conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
	if err != nil {
		t.Errorf("unexpected error when dialing: %s", err)
		return
	}
	defer conn.Close()

	client := NewClient(conn)

	var wg sync.WaitGroup

	for i := 0; i < 1000; i++ {
		wg.Add(1)

		go func(c Client) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			resp, err := c.Query(ctx, &Request{Query: "{ hello { world } }"})
			if err != nil {
				t.Errorf("unexpected error when querying: %s", err)
				return
			}

			var testResp struct {
				Hello struct {
					World string
				}
			}
			err = json.Unmarshal(resp.Data, &testResp)
			if err != nil {
				t.Logf("response data: %s", string(resp.Data))
				t.Errorf("unexpected error when unmarshalling response: %s", err)
				return
			}

			if testResp.Hello.World != "this is a test" {
				t.Logf("expected: %s, but got: %s", "this is a test", testResp.Hello.World)
				t.Fail()
				return
			}
		}(client)
	}

	wg.Wait()
}

func BenchmarkE2E(b *testing.B) {
	srv := httptest.NewServer(NewHandler(testHandler))
	defer srv.Close()

	b.RunParallel(func(pb *testing.PB) {
		conn, err := Dial(context.Background(), "ws://"+srv.Listener.Addr().String())
		if err != nil {
			b.Errorf("unexpected error when dialing: %s", err)
			return
		}
		defer conn.Close()

		client := NewClient(conn)

		for pb.Next() {
			_, err := client.Query(context.Background(), &Request{Query: "{ hello { world } }"})
			if err != nil {
				b.Errorf("unexpected error when querying: %s", err)
				return
			}
		}
	})
}
