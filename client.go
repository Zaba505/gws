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
		conn: conn,
		subs: make(map[opId]chan<- qResp),
		done: make(chan struct{}, 1),
	}

	c.subsMu.Lock()
	go c.run(c.subsMu.Unlock)

	return c
}

type client struct {
	conn *Conn

	id     uint64
	subsMu sync.Mutex
	subs   map[opId]chan<- qResp

	err  error
	done chan struct{}
}

// ErrUnexpectedMessage
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

func (c *client) run(unlock func()) {
	defer close(c.done)

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	err := c.conn.write(ctx, operationMessage{Type: gql_CONNECTION_INIT})
	cancel()
	if err != nil {
		unlock()
		c.err = ErrIO{
			Msg: "failed to send connection_init",
			Err: err,
		}
		return
	}

	ctx, cancel = context.WithTimeout(context.Background(), defaultTimeout)
	b, err := c.conn.read(ctx)
	unlock()
	cancel()
	if err != nil {
		c.err = ErrIO{
			Msg: "failed to receive connection_ack",
			Err: err,
		}
		return
	}

	ackMsg := new(operationMessage)
	err = ackMsg.UnmarshalJSON(b)
	if err != nil {
		c.err = ErrIO{
			Msg: "malformed operation message received",
			Err: err,
		}
		return
	}
	if ackMsg.Type != gql_CONNECTION_ACK {
		c.err = ErrUnexpectedMessage{
			Expected: gql_CONNECTION_ACK,
			Received: string(ackMsg.Type),
		}
		return
	}

	msg := new(operationMessage)
	for {
		ctx, cancel = context.WithTimeout(context.Background(), defaultTimeout)
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
			c.err = ErrIO{
				Msg: "malformed operation message received",
				Err: err,
			}
			return
		}

		c.subsMu.Lock()
		respCh := c.subs[msg.Id]
		delete(c.subs, msg.Id)
		c.subsMu.Unlock()

		respCh <- qResp{resp: msg.Payload.(*Response)}
		close(respCh)
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
	id := atomic.AddUint64(&c.id, 1)
	oid := opId(strconv.FormatUint(id, 10))
	msg := operationMessage{
		Id:      oid,
		Type:    gql_START,
		Payload: req,
	}
	id++

	respCh := make(chan qResp, 1)

	c.subsMu.Lock()
	c.subs[oid] = respCh
	c.subsMu.Unlock()

	err := c.conn.write(ctx, msg)
	if err != nil {
		return nil, err
	}

	select {
	case <-c.done:
		return nil, c.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-respCh:
		return resp.resp, resp.err
	}
}
