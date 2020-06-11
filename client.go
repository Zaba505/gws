package graphql_transport_ws

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
)

// Client
type Client interface {
	// Query
	Query(context.Context, *Request) (*Response, error)
}

// NewClient
func NewClient(conn *Conn) Client {
	c := &client{
		conn: conn,
		subs: make(map[opId]chan<- qResp),
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
}

func (c *client) run(unlock func()) {
	err := c.conn.write(context.TODO(), operationMessage{Type: gql_CONNECTION_INIT})
	if err != nil {
		unlock()
		return
	}

	b, err := c.conn.read(context.TODO())
	unlock()
	if err != nil {
		// TODO
		return
	}

	ackMsg := new(operationMessage)
	err = ackMsg.UnmarshalJSON(b)
	if ackMsg.Type != gql_CONNECTION_ACK {
		// TODO: Fail any in flight queries
		return
	}
	// TODO: defer close(c.reqs) after all in-flight queries have been cancelled

	msg := new(operationMessage)
	for {
		b, err := c.conn.read(context.TODO())
		if err != nil {
			// TODO
			return
		}

		err = msg.UnmarshalJSON(b)
		if err != nil {
			// TODO
			continue
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
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-respCh:
		return resp.resp, resp.err
	}
}
