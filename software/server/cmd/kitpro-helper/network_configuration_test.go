package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/protocol"
)

func TestNetworkConfigurationHelperRejectsForgedAndDisabledRequests(t *testing.T) {
	for _, request := range []protocol.Request{
		{ID: "request-one", OperationID: "op-0123456789abcdef0123456789abcdef", NetworkAction: "apply", NetworkResourceID: "../primary", ConfirmationInterface: "ens18"},
		{ID: "request-two", OperationID: "op-0123456789abcdef0123456789abcdef", NetworkAction: "apply", NetworkResourceID: "qualification", ConfirmationInterface: "kitpro-test0"},
	} {
		var framed bytes.Buffer
		manageNetworkConfiguration(&framed, request)
		response, err := protocol.ReadResponse(&framed)
		if err != nil {
			t.Fatal(err)
		}
		if response.OK || response.State != "rejected" || (!strings.Contains(response.ErrorCode, "Invalid") && !strings.Contains(response.ErrorCode, "Denied")) {
			t.Fatalf("response=%+v", response)
		}
	}
}
