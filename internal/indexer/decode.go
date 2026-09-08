// Package indexer decodes Soroban contract events into typed Go structs and
// applies them to the read model.
package indexer

import (
	"encoding/base64"
	"fmt"

	"github.com/stellar/go/xdr"
)

// Event names published by the two contracts. These match the #[contractevent]
// struct names in tuitio-contract.
// The contractevent macro emits the event name as a snake_case symbol, e.g.
// GrantCreated -> "grant_created". Verified against live testnet events.
const (
	EventInstitutionRegistered = "institution_registered"
	EventInstitutionVerified   = "institution_verified"
	EventInstitutionSuspended  = "institution_suspended"
	EventPayoutUpdated         = "payout_updated"
	EventGrantCreated          = "grant_created"
	EventTermAttested          = "term_attested"
	EventTermReleased          = "term_released"
	EventTermDisputed          = "term_disputed"
	EventDisputeResolved       = "dispute_resolved"
	EventTermRefunded          = "term_refunded"
	EventGrantCancelled        = "grant_cancelled"
	EventGrantCompleted        = "grant_completed"
)

// Typed mirrors of the contract events. Field names match the contract
// event fields, which the contractevent macro emits as map keys.
type InstitutionRegistered struct {
	Institution  string
	Payout       string
	Name         string
	Country      string
	RegisteredAt uint64
}

type InstitutionVerified struct {
	Institution string
	VerifiedAt  uint64
}

type InstitutionSuspended struct {
	Institution string
}

type PayoutUpdated struct {
	Institution string
	OldPayout   string
	NewPayout   string
}

type GrantCreated struct {
	GrantID     uint64
	Sponsor     string
	Institution string
	Beneficiary string
	Token       string
	TermAmount  int64
	TermsTotal  uint32
	TotalFunded int64
}

type TermAttested struct {
	GrantID      uint64
	TermIndex    uint32
	Institution  string
	AttestedAt   uint64
	ReleaseAfter uint64
}

type TermReleased struct {
	GrantID   uint64
	TermIndex uint32
	Payout    string
	Amount    int64
}

type TermDisputed struct {
	GrantID    uint64
	TermIndex  uint32
	Sponsor    string
	DisputedAt uint64
}

type DisputeResolved struct {
	GrantID   uint64
	TermIndex uint32
	Released  bool
	Amount    int64
}

type TermRefunded struct {
	GrantID   uint64
	TermIndex uint32
	Sponsor   string
	Amount    int64
}

type GrantCancelled struct {
	GrantID        uint64
	Sponsor        string
	TermsRefunded  uint32
	AmountRefunded int64
}

type GrantCompleted struct {
	GrantID    uint64
	TermsTotal uint32
}

// decodeScVal parses a base64 XDR ScVal.
func decodeScVal(b64 string) (xdr.ScVal, error) {
	var v xdr.ScVal
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return v, fmt.Errorf("base64 decode: %w", err)
	}
	if err := xdr.SafeUnmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("xdr decode: %w", err)
	}
	return v, nil
}

// symbolString reads a ScVal symbol.
func symbolString(v xdr.ScVal) (string, error) {
	if v.Type != xdr.ScValTypeScvSymbol {
		return "", fmt.Errorf("expected symbol, got %v", v.Type)
	}
	return string(*v.Sym), nil
}

// mapFields converts a ScVal map into string-keyed ScVal values.
func mapFields(v xdr.ScVal) (map[string]xdr.ScVal, error) {
	if v.Type != xdr.ScValTypeScvMap {
		return nil, fmt.Errorf("expected map, got %v", v.Type)
	}
	entries := *(*v.Map)
	out := make(map[string]xdr.ScVal, len(entries))
	for _, entry := range entries {
		key, err := symbolString(entry.Key)
		if err != nil {
			return nil, fmt.Errorf("map key: %w", err)
		}
		out[key] = entry.Val
	}
	return out, nil
}

func addressString(v xdr.ScVal) (string, error) {
	if v.Type != xdr.ScValTypeScvAddress {
		return "", fmt.Errorf("expected address, got %v", v.Type)
	}
	return v.Address.String()
}

func u64Value(v xdr.ScVal) (uint64, error) {
	if v.Type != xdr.ScValTypeScvU64 {
		return 0, fmt.Errorf("expected u64, got %v", v.Type)
	}
	return uint64(*v.U64), nil
}

func u32Value(v xdr.ScVal) (uint32, error) {
	if v.Type != xdr.ScValTypeScvU32 {
		return 0, fmt.Errorf("expected u32, got %v", v.Type)
	}
	return uint32(*v.U32), nil
}

func i128Value(v xdr.ScVal) (int64, error) {
	if v.Type != xdr.ScValTypeScvI128 {
		return 0, fmt.Errorf("expected i128, got %v", v.Type)
	}
	lo := uint64(v.I128.Lo)
	hi := int64(v.I128.Hi)
	// Tuitio amounts fit in int64; anything larger is a data problem worth
	// failing on rather than silently truncating.
	if hi != 0 && hi != -1 {
		return 0, fmt.Errorf("i128 exceeds int64 range (hi=%d)", hi)
	}
	n := int64(lo)
	if hi == -1 {
		n = -n
	}
	return n, nil
}

func boolValue(v xdr.ScVal) (bool, error) {
	if v.Type != xdr.ScValTypeScvBool {
		return false, fmt.Errorf("expected bool, got %v", v.Type)
	}
	return *v.B, nil
}

func stringValue(v xdr.ScVal) (string, error) {
	if v.Type != xdr.ScValTypeScvString {
		return "", fmt.Errorf("expected string, got %v", v.Type)
	}
	return string(*v.Str), nil
}

// topicVal decodes topics[i] as an ScVal; missing indices yield a zero ScVal.
type topicDecoder struct {
	topics []string
}

func (t topicDecoder) val(i int) xdr.ScVal {
	if i >= len(t.topics) {
		return xdr.ScVal{}
	}
	v, _ := decodeScVal(t.topics[i])
	return v
}

func (t topicDecoder) addr(i int) string {
	s, err := addressString(t.val(i))
	if err != nil {
		panic(fmt.Sprintf("topic[%d]: %v", i, err))
	}
	return s
}

func (t topicDecoder) u64(i int) uint64 {
	v, err := u64Value(t.val(i))
	if err != nil {
		panic(fmt.Sprintf("topic[%d]: %v", i, err))
	}
	return v
}

func (t topicDecoder) u32(i int) uint32 {
	v, err := u32Value(t.val(i))
	if err != nil {
		panic(fmt.Sprintf("topic[%d]: %v", i, err))
	}
	return v
}

func mustAddr(m map[string]xdr.ScVal, key string) string {
	s, err := addressString(m[key])
	if err != nil {
		panic(fmt.Sprintf("field %s: %v", key, err))
	}
	return s
}

func mustU64(m map[string]xdr.ScVal, key string) uint64 {
	v, err := u64Value(m[key])
	if err != nil {
		panic(fmt.Sprintf("field %s: %v", key, err))
	}
	return v
}

func mustU32(m map[string]xdr.ScVal, key string) uint32 {
	v, err := u32Value(m[key])
	if err != nil {
		panic(fmt.Sprintf("field %s: %v", key, err))
	}
	return v
}

func mustI128(m map[string]xdr.ScVal, key string) int64 {
	v, err := i128Value(m[key])
	if err != nil {
		panic(fmt.Sprintf("field %s: %v", key, err))
	}
	return v
}

func mustBool(m map[string]xdr.ScVal, key string) bool {
	v, err := boolValue(m[key])
	if err != nil {
		panic(fmt.Sprintf("field %s: %v", key, err))
	}
	return v
}

func mustStr(m map[string]xdr.ScVal, key string) string {
	s, err := stringValue(m[key])
	if err != nil {
		panic(fmt.Sprintf("field %s: %v", key, err))
	}
	return s
}

// Decode parses one RPC event into a typed event. Topics carry the fields the
// contract marked #[topic]; the value map carries the rest. The any return is
// nil for informational events the indexer does not act on.
func Decode(name string, topics []string, valueB64 string) (any, error) {
	v, err := decodeScVal(valueB64)
	if err != nil {
		return nil, err
	}
	m, err := mapFields(v)
	if err != nil {
		return nil, fmt.Errorf("event %s: %w", name, err)
	}
	t := topicDecoder{topics: topics}

	switch name {
	case EventInstitutionRegistered:
		return InstitutionRegistered{
			Institution:  t.addr(1),
			Payout:       mustAddr(m, "payout"),
			Name:         mustStr(m, "name"),
			Country:      mustStr(m, "country"),
			RegisteredAt: mustU64(m, "registered_at"),
		}, nil
	case EventInstitutionVerified:
		return InstitutionVerified{
			Institution: t.addr(1),
			VerifiedAt:  mustU64(m, "verified_at"),
		}, nil
	case EventInstitutionSuspended:
		return InstitutionSuspended{
			Institution: t.addr(1),
		}, nil
	case EventPayoutUpdated:
		return PayoutUpdated{
			Institution: t.addr(1),
			OldPayout:   mustAddr(m, "old_payout"),
			NewPayout:   mustAddr(m, "new_payout"),
		}, nil
	case EventGrantCreated:
		return GrantCreated{
			GrantID:     t.u64(1),
			Sponsor:     t.addr(2),
			Institution: t.addr(3),
			Beneficiary: mustAddr(m, "beneficiary"),
			Token:       mustAddr(m, "token"),
			TermAmount:  mustI128(m, "term_amount"),
			TermsTotal:  mustU32(m, "terms_total"),
			TotalFunded: mustI128(m, "total_funded"),
		}, nil
	case EventTermAttested:
		return TermAttested{
			GrantID:      t.u64(1),
			TermIndex:    t.u32(2),
			Institution:  mustAddr(m, "institution"),
			AttestedAt:   mustU64(m, "attested_at"),
			ReleaseAfter: mustU64(m, "release_after"),
		}, nil
	case EventTermReleased:
		return TermReleased{
			GrantID:   t.u64(1),
			TermIndex: t.u32(2),
			Payout:    mustAddr(m, "payout"),
			Amount:    mustI128(m, "amount"),
		}, nil
	case EventTermDisputed:
		return TermDisputed{
			GrantID:    t.u64(1),
			TermIndex:  t.u32(2),
			Sponsor:    mustAddr(m, "sponsor"),
			DisputedAt: mustU64(m, "disputed_at"),
		}, nil
	case EventDisputeResolved:
		return DisputeResolved{
			GrantID:   t.u64(1),
			TermIndex: t.u32(2),
			Released:  mustBool(m, "released"),
			Amount:    mustI128(m, "amount"),
		}, nil
	case EventTermRefunded:
		return TermRefunded{
			GrantID:   t.u64(1),
			TermIndex: t.u32(2),
			Sponsor:   mustAddr(m, "sponsor"),
			Amount:    mustI128(m, "amount"),
		}, nil
	case EventGrantCancelled:
		return GrantCancelled{
			GrantID:        t.u64(1),
			Sponsor:        mustAddr(m, "sponsor"),
			TermsRefunded:  mustU32(m, "terms_refunded"),
			AmountRefunded: mustI128(m, "amount_refunded"),
		}, nil
	case EventGrantCompleted:
		return GrantCompleted{
			GrantID:    t.u64(1),
			TermsTotal: mustU32(m, "terms_total"),
		}, nil
	}
	return nil, nil
}
