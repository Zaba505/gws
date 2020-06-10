package graphql_transport_ws

import (
	"context"
	"strconv"
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
		reqs: make(chan qReq),
	}

	go c.run()

	return c
}

type client struct {
	conn *Conn

	reqs chan qReq
}

func (c *client) run() {
	c.conn.send(context.TODO(), operationMessage{Type: gql_CONNECTION_INIT})

	msg := <-c.conn.in
	if msg.Type != gql_CONNECTION_ACK {
		// TODO: Fail any in flight queries
		return
	}
	// TODO: defer close(c.reqs) after all in-flight queries have been cancelled

	id := uint64(0)
	subs := make(map[opId]chan<- qResp)

	for req := range c.reqs {
		oid := opId(strconv.FormatUint(id, 10))
		msg := operationMessage{
			Id:      oid,
			Type:    gql_START,
			Payload: req,
		}
		subs[oid] = req.resp
		id++

		c.conn.out <- msg

		msg = <-c.conn.in
		respCh := subs[msg.Id]
		respCh <- qResp{resp: msg.Payload.(*Response)}
		close(respCh)
		delete(subs, msg.Id)
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
	r := qReq{
		Request: req,
		resp:    make(chan qResp, 1),
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case c.reqs <- r:
		resp := <-r.resp
		return resp.resp, resp.err
	}
}
