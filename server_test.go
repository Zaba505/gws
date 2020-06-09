package graphql_transport_ws

import (
	"encoding/json"
	"reflect"
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
    "data": {
      "hello": {
        "world": "this is a test"
      }
    }
  }
}
`,
			Payload: &Response{Data: map[string]interface{}{"hello": map[string]interface{}{"world": "this is a test"}}},
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
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(subT *testing.T) {
			msg := new(operationMessage)

			err := json.Unmarshal([]byte(testCase.JSON), msg)
			if err != nil && testCase.Err == nil {
				subT.Errorf("unexpected error when unmarshaling: %s", err)
				return
			}
			if err != nil && err != testCase.Err {
				subT.Logf("expected error: %s, but got: %s", testCase.Err, err)
				subT.Fail()
				return
			}

			if testCase.Payload == nil {
				return
			}

			if msg.Payload == nil {
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
				b.Error(err)
			}
		}
	})

	b.Run("Via json.Unmarshal", func(subB *testing.B) {
		for i := 0; i < subB.N; i++ {
			msg := new(operationMessage)
			err := json.Unmarshal([]byte(benchReq), msg)
			if err != nil {
				b.Error(err)
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

		if !reflect.DeepEqual(u, v) {
			t.Log("response aren't equal")
			t.Fail()
			return
		}
	case unknown:
		t.Log("expected payload is: unknown which should never happen")
		t.Fail()
	}
}
