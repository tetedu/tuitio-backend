// Package rpc is a minimal Soroban RPC client covering exactly the calls the
// indexer needs: getEvents and getLatestLedger.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	url  string
	http *http.Client
}

func New(url string) *Client {
	return &Client{
		url:  url,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

type rpcRequest struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      int       `json:"id"`
	Method  string    `json:"method"`
	Params  rpcParams `json:"params"`
}

type rpcParams struct {
	StartLedger uint32        `json:"startLedger"`
	Filters     []EventFilter `json:"filters,omitempty"`
	Limit       uint32        `json:"limit,omitempty"`
}

type EventFilter struct {
	Type        string   `json:"type"`
	ContractIDs []string `json:"contractIds,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// Event is one contract event as returned by getEvents.
type Event struct {
	ID             string   `json:"id"`
	Type           string   `json:"type"`
	Ledger         uint32   `json:"ledger"`
	LedgerClosedAt string   `json:"ledgerClosedAt"`
	ContractID     string   `json:"contractId"`
	Topic          []string `json:"topic"`
	Value          string   `json:"value"`
	TxHash         string   `json:"txHash"`
}

type getEventsResult struct {
	Events       []Event `json:"events"`
	LatestLedger uint32  `json:"latestLedger"`
	Cursor       string  `json:"cursor"`
}

type getLatestLedgerResult struct {
	ID string `json:"id"`
	// Some RPC builds report the tip as "latestLedger", others as "sequence".
	LatestLedger uint32 `json:"latestLedger"`
	Sequence     uint32 `json:"sequence"`
}

func (c *Client) call(ctx context.Context, method string, params rpcParams, out any) error {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", method, err)
	}
	defer resp.Body.Close()

	var parsed rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
	}
	if parsed.Error != nil {
		return parsed.Error
	}
	if err := json.Unmarshal(parsed.Result, out); err != nil {
		return fmt.Errorf("unmarshal %s result: %w", method, err)
	}
	return nil
}

// LatestLedger returns the current chain tip.
func (c *Client) LatestLedger(ctx context.Context) (uint32, error) {
	var res getLatestLedgerResult
	if err := c.call(ctx, "getLatestLedger", rpcParams{}, &res); err != nil {
		return 0, err
	}
	if res.LatestLedger != 0 {
		return res.LatestLedger, nil
	}
	return res.Sequence, nil
}

// Events returns contract events for the two Tuitio contracts starting at
// startLedger. The RPC caps the ledger span per request, so callers page by
// advancing startLedger past the highest ledger seen.
func (c *Client) Events(ctx context.Context, startLedger uint32, contracts []string) ([]Event, uint32, error) {
	var res getEventsResult
	params := rpcParams{
		StartLedger: startLedger,
		Filters:     []EventFilter{{Type: "contract", ContractIDs: contracts}},
		Limit:       100,
	}
	if err := c.call(ctx, "getEvents", params, &res); err != nil {
		return nil, 0, err
	}
	return res.Events, res.LatestLedger, nil
}
