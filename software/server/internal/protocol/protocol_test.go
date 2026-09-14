package protocol

import (
	"bytes"
	"testing"
)

func TestFrameRoundTripAndHash(t *testing.T) {
	r := Request{Version: 1, ID: "op-1", Operation: "Ping"}
	var b bytes.Buffer
	if err := Write(&b, r); err != nil {
		t.Fatal(err)
	}
	got, err := Read(&b)
	if err != nil || got.ID != r.ID {
		t.Fatalf("round trip: %#v %v", got, err)
	}
	h1, _ := Hash(r)
	h2, _ := Hash(r)
	if h1 != h2 {
		t.Fatal("hash not stable")
	}
}
func TestRejectsUnknown(t *testing.T) {
	var b bytes.Buffer
	b.Write([]byte{0, 0, 0, 35})
	b.WriteString(`{"version":1,"request_id":"x","operation":"Ping","extra":1}`)
	if _, err := Read(&b); err == nil {
		t.Fatal("accepted unknown field")
	}
}
