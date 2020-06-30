[![Go Report Card](https://goreportcard.com/badge/github.com/zaba505/gws)](https://goreportcard.com/report/github.com/zaba505/gws)
[![build](https://github.com/Zaba505/gws/workflows/build/badge.svg)](https://github.com/Zaba505/gws/actions)
[![GoDoc](https://godoc.org/github.com/Zaba505/gws?status.svg)](https://godoc.org/github.com/Zaba505/gws)

# gws
A WebSocket client and server for GraphQL.

`gws` implements Apollos' "GraphQL over WebSocket" protocol,
which can be found in PROTOCOL.md. It provides an RPC-like API for both
client and server usage.

## Getting Started

`gws` provides both a client and server implementation for the
"GraphQL over Websocket" protocol.

### Client

To create a client, you must first dial the server like:
```go
conn, err := gws.Dial(context.TODO(), "ws://example.com")
// Don't forget to handle the error
```

`gws.Dial` will return high-level abstraction around the
underlying connection. This is very similar to gRPCs'
behaviour. After dialing the server, now simply wrap the
return conn and begin performing queries, like:
```go
client := gws.NewClient(conn)

resp, err := client.Query(context.TODO(), &gws.Request{Query: "{ hello { world } }"})
// Don't forget to handle the error

var data struct{
  Hello struct{
    World string
  }
}
err = json.Unmarshal(resp.Data, &data)
```

### Server

The server implementation provided isn't actually a standalone
server but instead just a specially configured `http.Handler`.
This is because the underlying protocol is simply WebSocket,
which runs on top of HTTP. Creating a bare bones handler is
as simple as the following:
```go
msgHandler := func(s *Stream, req *Request) error {
  // Handle request
  return nil
}

http.ListenAndServe(":8080", gws.NewHandler(gws.HandlerFunc(msgHandler)))
```
