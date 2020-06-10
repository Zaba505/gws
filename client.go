package graphql_transport_ws

import (
	"context"
)

// Client
type Client interface {
	// Query
	Query(context.Context, *Request) (*Response, error)
}

// NewClient
func NewClient(c *Conn) Client {
	return &wsClient{
		Conn: c,
	}
}

type wsClient struct {
	*Conn
}

func (c *wsClient) Query(ctx context.Context, req *Request) (*Response, error) {
	return nil, nil
}
