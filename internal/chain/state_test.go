package chain

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stellar/go/keypair"
	"github.com/stellar/go/strkey"
	"github.com/stellar/go/xdr"
)

func testContractID(t *testing.T) string {
	t.Helper()
	var raw [32]byte
	raw[0] = 0x42
	id, err := strkey.Encode(strkey.VersionByteContract, raw[:])
	if err != nil {
		t.Fatalf("encode contract id: %v", err)
	}
	return id
}

func TestAddressVec(t *testing.T) {
	kp1, err := keypair.Random()
	if err != nil {
		t.Fatalf("keypair 1: %v", err)
	}
	kp2, err := keypair.Random()
	if err != nil {
		t.Fatalf("keypair 2: %v", err)
	}
	a1, err := addressArg(kp1.Address())
	if err != nil {
		t.Fatalf("addressArg 1: %v", err)
	}
	a2, err := addressArg(kp2.Address())
	if err != nil {
		t.Fatalf("addressArg 2: %v", err)
	}

	vec := xdr.ScVec{a1, a2}
	ptr := &vec
	got, err := AddressVec(xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &ptr})
	if err != nil {
		t.Fatalf("AddressVec happy path: %v", err)
	}
	if len(got) != 2 || got[0] != kp1.Address() || got[1] != kp2.Address() {
		t.Fatalf("got %v, want [%s %s]", got, kp1.Address(), kp2.Address())
	}

	// Failure path 1: non-vec ScVal.
	if _, err := AddressVec(u32Arg(7)); err == nil {
		t.Fatal("expected error for non-vec ScVal")
	}

	// Failure path 2: vec containing a non-address element.
	badVec := xdr.ScVec{a1, u32Arg(9)}
	badPtr := &badVec
	if _, err := AddressVec(xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &badPtr}); err == nil {
		t.Fatal("expected error for vec containing non-address element")
	}
}

func TestRegistryListHappyAndFailurePaths(t *testing.T) {
	sourceKP, err := keypair.Random()
	if err != nil {
		t.Fatalf("source keypair: %v", err)
	}
	instKP, err := keypair.Random()
	if err != nil {
		t.Fatalf("inst keypair: %v", err)
	}
	contractID := testContractID(t)

	instVal, err := addressArg(instKP.Address())
	if err != nil {
		t.Fatalf("addressArg: %v", err)
	}
	vec := xdr.ScVec{instVal}
	vecPtr := &vec
	retBytes, err := (xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &vecPtr}).MarshalBinary()
	if err != nil {
		t.Fatalf("marshal ScVal: %v", err)
	}
	retB64 := base64.StdEncoding.EncodeToString(retBytes)

	var (
		seenFn    string
		seenStart uint32
		seenLimit uint32
		mode      = "ok"
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req simulateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode req: %v", err)
		}
		envBytes, err := base64.StdEncoding.DecodeString(req.Params["transaction"])
		if err != nil {
			t.Errorf("decode tx b64: %v", err)
		}
		var env xdr.TransactionEnvelope
		if err := xdr.SafeUnmarshal(envBytes, &env); err != nil {
			t.Errorf("unmarshal envelope: %v", err)
		}
		invoke := env.Operations()[0].Body.InvokeHostFunctionOp.HostFunction.InvokeContract
		seenFn = string(invoke.FunctionName)
		seenStart, _ = U32(invoke.Args[0])
		seenLimit, _ = U32(invoke.Args[1])

		w.Header().Set("Content-Type", "application/json")
		switch mode {
		case "ok":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{
					"results": []map[string]string{{"xdr": retB64}},
				},
			})
		case "trap":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{
					"error": "HostError: Error(WasmVm, MissingValue)",
				},
			})
		case "rpc_error":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"error": map[string]any{
					"code":    -32603,
					"message": "upstream unavailable",
				},
			})
		}
	}))
	defer srv.Close()

	reg := NewRegistry(NewReader(srv.URL, sourceKP.Address()), contractID)

	// Happy path + limit clamping above MaxRegistryPage.
	got, err := reg.List(context.Background(), 5, 500)
	if err != nil {
		t.Fatalf("List happy path: %v", err)
	}
	if seenFn != "list" || seenStart != 5 || seenLimit != MaxRegistryPage {
		t.Fatalf("call = %s(%d, %d), want list(5, %d)", seenFn, seenStart, seenLimit, MaxRegistryPage)
	}
	if len(got) != 1 || got[0] != instKP.Address() {
		t.Fatalf("got %v, want [%s]", got, instKP.Address())
	}

	// Failure path 1: contract trap (pre-list deployment) maps to ErrNotFound.
	mode = "trap"
	if _, err := reg.List(context.Background(), 0, MaxRegistryPage); !errors.Is(err, ErrNotFound) {
		t.Fatalf("contract trap err = %v, want ErrNotFound", err)
	}

	// Failure path 2: RPC error propagates without masking as ErrNotFound.
	mode = "rpc_error"
	_, err = reg.List(context.Background(), 0, MaxRegistryPage)
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("rpc error = %v, want non-ErrNotFound error", err)
	}
}
