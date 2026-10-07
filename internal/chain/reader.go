package chain

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/stellar/go/strkey"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/xdr"
)

// Reader calls read-only contract functions through the RPC's
// simulateTransaction, so no key material and no fees are involved.
type Reader struct {
	rpcURL string
	source string
	http   *http.Client
}

// NewReader builds a reader. source is any existing account address; it is
// only needed because a transaction envelope must name one, and simulation
// neither signs nor submits it.
func NewReader(rpcURL, source string) *Reader {
	return &Reader{
		rpcURL: rpcURL,
		source: source,
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

type simulateRequest struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      int               `json:"id"`
	Method  string            `json:"method"`
	Params  map[string]string `json:"params"`
}

type simulateResponse struct {
	Result *struct {
		Results []struct {
			XDR string `json:"xdr"`
		} `json:"results"`
		Error string `json:"error"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// contractAddress converts a C... strkey into the XDR address form.
func contractAddress(contractID string) (xdr.ScAddress, error) {
	raw, err := strkey.Decode(strkey.VersionByteContract, contractID)
	if err != nil {
		return xdr.ScAddress{}, fmt.Errorf("decode contract id: %w", err)
	}
	var id xdr.ContractId
	copy(id[:], raw)
	return xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &id,
	}, nil
}

// Call simulates a contract function and returns its raw return value.
func (r *Reader) Call(ctx context.Context, contractID, fn string, args ...xdr.ScVal) (xdr.ScVal, error) {
	var zero xdr.ScVal

	addr, err := contractAddress(contractID)
	if err != nil {
		return zero, err
	}
	if args == nil {
		args = []xdr.ScVal{}
	}

	op := &txnbuild.InvokeHostFunction{
		HostFunction: xdr.HostFunction{
			Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
			InvokeContract: &xdr.InvokeContractArgs{
				ContractAddress: addr,
				FunctionName:    xdr.ScSymbol(fn),
				Args:            args,
			},
		},
		SourceAccount: r.source,
	}

	account := txnbuild.NewSimpleAccount(r.source, 0)
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &account,
		IncrementSequenceNum: true,
		Operations:           []txnbuild.Operation{op},
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewInfiniteTimeout()},
	})
	if err != nil {
		return zero, fmt.Errorf("build tx: %w", err)
	}
	envelope, err := tx.Base64()
	if err != nil {
		return zero, fmt.Errorf("encode tx: %w", err)
	}

	body, err := json.Marshal(simulateRequest{
		JSONRPC: "2.0", ID: 1, Method: "simulateTransaction",
		Params: map[string]string{"transaction": envelope},
	})
	if err != nil {
		return zero, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.rpcURL, bytes.NewReader(body))
	if err != nil {
		return zero, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.http.Do(req)
	if err != nil {
		return zero, fmt.Errorf("simulate %s: %w", fn, err)
	}
	defer resp.Body.Close()

	var parsed simulateResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return zero, fmt.Errorf("decode simulate response: %w", err)
	}
	if parsed.Error != nil {
		return zero, fmt.Errorf("rpc error %d: %s", parsed.Error.Code, parsed.Error.Message)
	}
	if parsed.Result == nil {
		return zero, fmt.Errorf("simulate %s: empty result", fn)
	}
	if parsed.Result.Error != "" {
		// The contract trapped — for reads this means "no such entry".
		return zero, fmt.Errorf("%s: %w", fn, &ContractError{Detail: parsed.Result.Error})
	}
	if len(parsed.Result.Results) == 0 {
		return zero, fmt.Errorf("simulate %s: no return value", fn)
	}

	raw, err := base64.StdEncoding.DecodeString(parsed.Result.Results[0].XDR)
	if err != nil {
		return zero, fmt.Errorf("decode retval: %w", err)
	}
	var val xdr.ScVal
	if err := xdr.SafeUnmarshal(raw, &val); err != nil {
		return zero, fmt.Errorf("unmarshal retval: %w", err)
	}
	return val, nil
}

// ContractError reports that the contract itself rejected the call, which for
// a read means the entry does not exist.
type ContractError struct {
	Detail string
}

func (e *ContractError) Error() string { return "contract error: " + e.Detail }

func u64Arg(n uint64) xdr.ScVal {
	v := xdr.Uint64(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &v}
}

func u32Arg(n uint32) xdr.ScVal {
	v := xdr.Uint32(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &v}
}

func addressArg(addr string) (xdr.ScVal, error) {
	raw, err := strkey.Decode(strkey.VersionByteAccountID, addr)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("decode address: %w", err)
	}
	var key xdr.Uint256
	copy(key[:], raw)
	aid := xdr.AccountId{
		Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
		Ed25519: &key,
	}
	sa := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &aid}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &sa}, nil
}
