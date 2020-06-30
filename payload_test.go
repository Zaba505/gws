package gws

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestOpMessage_Unmarshal(t *testing.T) {
	testCases := []struct {
		Name    string
		JSON    string
		Payload payload
		Err     error
	}{
		{
			Name: "NoPayload",
			JSON: `
{
  "type": "connection_init"
}
`,
		},
		{
			Name: "WithRequest",
			JSON: `
{
  "id": "1",
  "type": "start",
  "payload": {
    "query": "{ hello { world } }"
  }
}`,
			Payload: &Request{Query: "{ hello { world } }"},
		},
		{
			Name: "WithResponse",
			JSON: `
{
  "id": "1",
  "type": "data",
  "payload": {
    "data": {"hello":{"world":"this is a test"}}
  }
}
`,
			Payload: &Response{Data: json.RawMessage([]byte(`{"hello":{"world":"this is a test"}}`))},
		},
		{
			Name: "Unordered",
			JSON: `
{
  "id": "1",
  "payload": {
    "query": "{ hello { world } }"
  },
  "type": "start"
}`,
			Payload: &Request{Query: "{ hello { world } }"},
		},
		{
			Name: "UnsupportedType",
			JSON: `{"type":"asdgf", "payload": {}}`,
			Err:  ErrUnsupportedMsgType("asdgf"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(subT *testing.T) {
			msg := new(operationMessage)

			err := json.Unmarshal([]byte(testCase.JSON), msg)
			t.Log(err)
			switch {
			case err != nil && testCase.Err == nil:
				subT.Errorf("unexpected error when unmarshaling: %s", err)
				return
			case err == nil && testCase.Err != nil:
				subT.Errorf("expected error: %s", testCase.Err)
				return
			case err != nil && !errors.Is(err, testCase.Err):
				subT.Logf("expected error: %s, but got: %s", testCase.Err, err)
				subT.Fail()
				return
			case testCase.Payload == nil:
				return
			case msg.Payload == nil:
				subT.Logf("expected payload: %v, but got nothing", testCase.Payload)
				subT.Fail()
				return
			}

			comparePayloads(subT, testCase.Payload, msg.Payload)
		})
	}
}

const benchReq = `{
  "id": "1",
  "type": "start",
  "payload": {
    "query": "{ hello { world } }"
  }
}`

func BenchmarkOpMessage_Unmarshal(b *testing.B) {
	b.Run("Via UnmarshalJSON", func(subB *testing.B) {
		for i := 0; i < subB.N; i++ {
			msg := new(operationMessage)
			err := msg.UnmarshalJSON([]byte(benchReq))
			if err != nil {
				subB.Error(err)
			}
		}
	})

	b.Run("Via json.Unmarshal", func(subB *testing.B) {
		for i := 0; i < subB.N; i++ {
			msg := new(operationMessage)
			err := json.Unmarshal([]byte(benchReq), msg)
			if err != nil {
				subB.Error(err)
			}
		}
	})
}

func comparePayloads(t *testing.T, ex, out payload) {
	t.Helper()

	switch u := ex.(type) {
	case *Request:
		v, ok := out.(*Request)
		if !ok {
			t.Logf("expected payload: request, but got: %#v", out)
			t.Fail()
			return
		}

		if v.Query != u.Query || v.OperationName != u.OperationName {
			t.Logf("requests aren't equal: %v::%v", u, v)
			t.Fail()
			return
		}
	case *Response:
		v, ok := out.(*Response)
		if !ok {
			t.Logf("expected payload: request, but got: %#v", out)
			t.Fail()
			return
		}

		if !bytes.Equal(u.Data, v.Data) {
			t.Logf("expected data: %s, but got: %s", string(u.Data), string(v.Data))
			t.Fail()
			return
		}
	case unknown:
		t.Log("expected payload is: unknown which should never happen")
		t.Fail()
	}
}
