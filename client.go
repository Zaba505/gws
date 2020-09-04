// Package gws implements a client and server for the GraphQL over Websocket protocol.
package gws

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// ErrUnsubscribed is returned by a subscription receive when the subscription
// is unsubscribed to or completed before the next response is received.
//
var ErrUnsubscribed = errors.New("gws: received cancelled due to unsubscribe")

// Client provides high-level API for making GraphQL requests over WebSocket.
type Client interface {
	// Query provides an RPC like API for performing GraphQL queries.
	Query(context.Context, *Request) (*Response, error)

	// Subscribe provides an RPC like API for performing GraphQL subscription queries.
	Subscribe(context.Context, *Request) (*Subscription, error)
}

// Subscription represents a stream of results corresponding to a GraphQL subscription query.
type Subscription struct {
	conn   *Conn
	id     opID
	respCh <-chan qResp

	// used to cancel in-flight recv on unsubscribe
	done chan struct{}
}

// Recv is a blocking call which waits for either a response from the
// server or the context to be cancelled. Context cancellation does
// not cancel the subscription as a whole just the current recv call.
//
// Recv is safe for concurrent use but it is a first come first serve
// basis. In other words, the response is not duplicated across all
// receivers.
//
func (s *Subscription) Recv(ctx context.Context) (*Response, error) {
	select {
	case <-s.done:
		return nil, ErrUnsubscribed
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp, ok := <-s.respCh:
		if !ok {
			return nil, ErrUnsubscribed
		}
		return resp.resp, resp.err
	}
}

// Unsubscribe tells the server to stop sending anymore results
// and cleans up any resources associated with the subscription.
//
func (s *Subscription) Unsubscribe() error {
	close(s.done)

	select {
	case <-s.respCh:
		// Already completed by the server
		return nil
	default:
	}

	err := s.conn.write(context.TODO(), operationMessage{ID: s.id, Type: gqlStop})
	if err != nil {
		return ErrIO{
			Msg: "failed to send stop message for: " + string(s.id),
			Err: err,
		}
	}
	return nil
}

// NewClient takes a connection and initializes a client over it.
func NewClient(conn *Conn) Client {
	c := &client{
		conn:  conn,
		subs:  make(map[opID]chan<- qResp),
		ready: make(chan struct{}, 1),
		done:  make(chan struct{}, 1),
	}

	go c.run()

	return c
}

type client struct {
	conn *Conn

	id     uint64
	subsMu sync.Mutex
	subs   map[opID]chan<- qResp

	err   error
	ready chan struct{}
	done  chan struct{}
}

// ErrUnexpectedMessage represents a unexpected message type.
type ErrUnexpectedMessage struct {
	// Expected was the expected message type.
	Expected string

	// Received was the received message type.
	Received string
}

// Error implements the error interface.
func (e ErrUnexpectedMessage) Error() string {
	b := new(bytes.Buffer)
	b.Write([]byte("unexpected message type received: "))
	b.WriteString(e.Received)
	b.Write([]byte(":"))
	b.WriteString(e.Expected)
	return b.String()
}

// ErrIO represents a wrapped I/O error.
type ErrIO struct {
	// Msg
	Msg string

	// Err
	Err error
}

// Error implements the error interface.
func (e ErrIO) Error() string {
	b := new(bytes.Buffer)
	b.WriteString(e.Msg)
	b.Write([]byte(": "))
	b.WriteString(e.Err.Error())
	return b.String()
}

// Unwrap is for the errors package to use within its As, Is, and Unwrap functions.
func (e ErrIO) Unwrap() error {
	return e.Err
}

const defaultTimeout = 5 * time.Second

func (c *client) initConn(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	err := c.conn.write(ctx, operationMessage{Type: gqlConnectionInit})
	cancel()
	if err != nil {
		return ErrIO{
			Msg: "failed to send connection_init",
			Err: err,
		}
	}

	ctx, cancel = context.WithTimeout(context.Background(), defaultTimeout)
	b, err := c.conn.read(ctx)
	cancel()
	if err != nil {
		return ErrIO{
			Msg: "failed to receive connection_ack",
			Err: err,
		}
	}

	ackMsg := new(operationMessage)
	err = ackMsg.UnmarshalJSON(b)
	if err != nil {
		return err
	}
	if ackMsg.Type != gqlConnectionAck {
		return ErrUnexpectedMessage{
			Expected: string(gqlConnectionAck),
			Received: string(ackMsg.Type),
		}
	}

	return nil
}

func (c *client) processMessages(msgs <-chan operationMessage) {
	var err error
	for msg := range msgs {
		switch msg.Type {
		case gqlData, gqlError:
			r, ok := msg.Payload.(*Response)
			if !ok {
				err, _ = msg.Payload.(*ServerError)
			}

			c.subsMu.Lock()
			respCh := c.subs[msg.ID]
			c.subsMu.Unlock()

			respCh <- qResp{resp: r, err: err}
		case gqlComplete:
			c.subsMu.Lock()
			respCh := c.subs[msg.ID]
			delete(c.subs, msg.ID)
			c.subsMu.Unlock()

			close(respCh)
		}
	}

	c.subsMu.Lock()
	defer c.subsMu.Unlock()

	for _, respCh := range c.subs {
		close(respCh)
	}
}

func (c *client) run() {
	defer close(c.done)

	err := c.initConn(defaultTimeout)
	if err != nil {
		c.err = err
		return
	}
	close(c.ready)

	msgs := make(chan operationMessage, 1)
	defer close(msgs)

	go c.processMessages(msgs)

	msg := new(operationMessage)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
		b, err := c.conn.read(ctx)
		cancel()
		if err != nil {
			c.err = ErrIO{
				Msg: "failed to read",
				Err: err,
			}
			return
		}

		err = msg.UnmarshalJSON(b)
		if err != nil {
			c.err = err
			return
		}

		msgs <- *msg

		msg.ID = ""
		msg.Payload = nil
		msg.Type = ""
	}
}

type qResp struct {
	resp *Response
	err  error
}

type qReq struct {
	*Request
	resp chan qResp
}

func (c *client) Query(ctx context.Context, req *Request) (*Response, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ready:
		break
	case <-c.done:
		return nil, c.err
	}

	id := atomic.AddUint64(&c.id, 1)
	oid := opID(strconv.FormatUint(id, 10))
	msg := operationMessage{
		ID:      oid,
		Type:    gqlStart,
		Payload: req,
	}

	respCh := make(chan qResp, 1)

	c.subsMu.Lock()
	c.subs[oid] = respCh
	c.subsMu.Unlock()

	err := c.conn.write(ctx, msg)
	if err != nil {
		return nil, ErrIO{
			Msg: "failed to send query",
			Err: err,
		}
	}

	select {
	case <-c.done:
		return nil, c.err
	case <-ctx.Done():
		go stopReq(c.conn, oid, respCh)
		return nil, ctx.Err()
	case resp, ok := <-respCh:
		if !ok {
			return nil, c.err
		}
		return resp.resp, resp.err
	}
}

func (c *client) Subscribe(ctx context.Context, req *Request) (*Subscription, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ready:
		break
	case <-c.done:
		return nil, c.err
	}

	id := atomic.AddUint64(&c.id, 1)
	oid := opID(strconv.FormatUint(id, 10))
	msg := operationMessage{
		ID:      oid,
		Type:    gqlStart,
		Payload: req,
	}

	respCh := make(chan qResp, 1)

	c.subsMu.Lock()
	c.subs[oid] = respCh
	c.subsMu.Unlock()

	err := c.conn.write(ctx, msg)
	if err != nil {
		return nil, ErrIO{
			Msg: "failed to send query",
			Err: err,
		}
	}

	return &Subscription{
		conn:   c.conn,
		id:     oid,
		respCh: respCh,
		done:   make(chan struct{}, 1),
	}, nil
}

func stopReq(conn *Conn, id opID, respCh <-chan qResp) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := conn.write(ctx, operationMessage{ID: id, Type: gqlStop})
	if err != nil {
		return
	}

	// TODO: Should a local COMPLETE message be sent to processMessages?

	// In case an in-flight DATA message was received it should be drained.
	<-respCh
}
