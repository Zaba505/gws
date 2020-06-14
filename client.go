package graphql_transport_ws

import (
	"bytes"
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Client provides high-level API for making GraphQL requests over WebSocket.
type Client interface {
	// Query provides an RPC like API for performing GraphQL queries.
	Query(context.Context, *Request) (*Response, error)
}

// NewClient takes a connection and initializes a client over it.
func NewClient(conn *Conn) Client {
	c := &client{
		conn:  conn,
		subs:  make(map[opId]chan<- qResp),
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
	subs   map[opId]chan<- qResp

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
	err := c.conn.write(ctx, operationMessage{Type: gql_CONNECTION_INIT})
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
	if ackMsg.Type != gql_CONNECTION_ACK {
		return ErrUnexpectedMessage{
			Expected: gql_CONNECTION_ACK,
			Received: string(ackMsg.Type),
		}
	}

	return nil
}

func (c *client) processMessages(msgs <-chan operationMessage) {
	var err error
	for msg := range msgs {
		switch msg.Type {
		case gql_DATA, gql_ERROR:
			r, ok := msg.Payload.(*Response)
			if !ok {
				err, _ = msg.Payload.(*ServerError)
			}

			c.subsMu.Lock()
			respCh := c.subs[msg.Id]
			c.subsMu.Unlock()

			respCh <- qResp{resp: r, err: err}
		case gql_COMPLETE:
			c.subsMu.Lock()
			respCh := c.subs[msg.Id]
			delete(c.subs, msg.Id)
			c.subsMu.Unlock()

			close(respCh)
		}
	}

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

		msg.Id = ""
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
	oid := opId(strconv.FormatUint(id, 10))
	msg := operationMessage{
		Id:      oid,
		Type:    gql_START,
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

func stopReq(conn *Conn, id opId, respCh <-chan qResp) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := conn.write(ctx, operationMessage{Id: id, Type: gql_STOP})
	if err != nil {
		return
	}

	// TODO: Should a local COMPLETE message be sent to processMessages?

	// In case an in-flight DATA message was received it should be drained.
	<-respCh
}
