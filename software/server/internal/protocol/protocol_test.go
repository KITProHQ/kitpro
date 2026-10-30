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

func TestCanonicalHashExcludesTransportAndGeneratedSecretValues(t *testing.T) {
	first := Request{Version: 2, ID: "req-one", OperationID: "op-11111111111111111111111111111111", Operation: "InstallApplication", OperationRevision: 1, Deadline: "2026-01-01T00:00:00Z", InstanceID: "inst-example01", Command: nil, Environment: []EnvVar{{Name: "TOKEN", Value: "generated-one", Secret: true, Generate: "random-hex-32"}}}
	second := first
	second.ID = "req-two"
	second.OperationID = "op-22222222222222222222222222222222"
	second.Deadline = "2027-01-01T00:00:00Z"
	second.Command = []string{}
	second.Environment = []EnvVar{{Name: "TOKEN", Value: "generated-two", Secret: true, Generate: "random-hex-32"}}
	firstHash, err := Hash(first)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := Hash(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("transport-only or equivalent values changed hash: %s != %s", firstHash, secondHash)
	}
	second.Command = []string{"--different"}
	changedHash, err := Hash(second)
	if err != nil {
		t.Fatal(err)
	}
	if changedHash == firstHash {
		t.Fatal("semantic command change did not alter hash")
	}
}

func TestRepairActionParticipatesInCanonicalHash(t *testing.T) {
	request := Request{Version: 2, ID: "req-one", OperationID: "op-11111111111111111111111111111111", Operation: "RepairInstallation", OperationRevision: 1, InstanceID: "inst-example01", RepairAction: "start_active"}
	first, err := Hash(request)
	if err != nil {
		t.Fatal(err)
	}
	request.RepairAction = "cleanup_resources"
	second, err := Hash(request)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("different repair semantics produced the same canonical hash")
	}
}

func TestRejectsDuplicateKeysAtAnyDepth(t *testing.T) {
	for _, payload := range []string{
		`{"version":2,"request_id":"one","request_id":"two","operation":"Ping"}`,
		`{"version":2,"request_id":"one","operation":"Ping","environment":[{"name":"A","name":"B"}]}`,
	} {
		var frame bytes.Buffer
		frame.Write([]byte{0, 0, 0, byte(len(payload))})
		frame.WriteString(payload)
		if _, err := Read(&frame); err == nil {
			t.Fatalf("accepted duplicate keys: %s", payload)
		}
	}
}
