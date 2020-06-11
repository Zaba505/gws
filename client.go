package graphql_transport_ws

import (
	"context"
	"strconv"
	"sync"
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
	c.conn.write(context.TODO(), operationMessage{Type: gql_CONNECTION_INIT})

	b, err := c.conn.read(context.TODO())
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

	var subsMu sync.Mutex
	id := uint64(0)
	subs := make(map[opId]chan<- qResp)

	go func() {
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

			subsMu.Lock()
			respCh := subs[msg.Id]
			delete(subs, msg.Id)
			subsMu.Unlock()

			respCh <- qResp{resp: msg.Payload.(*Response)}
			close(respCh)
		}
	}()

	for req := range c.reqs {
		oid := opId(strconv.FormatUint(id, 10))
		msg := operationMessage{
			Id:      oid,
			Type:    gql_START,
			Payload: req,
		}
		id++

		subsMu.Lock()
		subs[oid] = req.resp
		subsMu.Unlock()

		err := c.conn.write(context.TODO(), msg)
		if err != nil {
			break
		}
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
