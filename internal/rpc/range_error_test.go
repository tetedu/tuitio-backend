package rpc

import (
	"errors"
	"testing"
)

// The message format below is the real one returned by the Soroban testnet
// RPC when startLedger predates its retention window.
func TestAsRangeErrorParsesRetentionWindow(t *testing.T) {
	e := &rpcError{Code: -32600, Message: "startLedger must be within the ledger range: 4674323 - 4795282"}
	err := asRangeError(e)

	var re *RangeError
	if !errors.As(err, &re) {
		t.Fatalf("expected a RangeError, got %T", err)
	}
	if re.Oldest != 4674323 {
		t.Errorf("oldest = %d, want 4674323", re.Oldest)
	}
	if re.Newest != 4795282 {
		t.Errorf("newest = %d, want 4795282", re.Newest)
	}
}

func TestAsRangeErrorLeavesOtherErrorsAlone(t *testing.T) {
	for _, msg := range []string{
		"method not found",
		"invalid filter",
		"startLedger must be within the ledger range",
	} {
		err := asRangeError(&rpcError{Code: -32600, Message: msg})
		var re *RangeError
		if errors.As(err, &re) {
			t.Errorf("%q was wrongly treated as a range error", msg)
		}
	}
}
