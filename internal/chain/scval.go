// Package chain reads authoritative contract state directly from the Soroban
// RPC, so the indexed read model can be checked against the chain rather than
// trusted blindly.
package chain

import (
	"fmt"

	"github.com/stellar/go/xdr"
)

// Field reads a named field out of a contracttype struct, which Soroban
// encodes as a map keyed by symbols.
func Field(v xdr.ScVal, name string) (xdr.ScVal, error) {
	if v.Type != xdr.ScValTypeScvMap || v.Map == nil {
		return xdr.ScVal{}, fmt.Errorf("expected a struct map, got %v", v.Type)
	}
	for _, e := range *(*v.Map) {
		if e.Key.Type != xdr.ScValTypeScvSymbol || e.Key.Sym == nil {
			continue
		}
		if string(*e.Key.Sym) == name {
			return e.Val, nil
		}
	}
	return xdr.ScVal{}, fmt.Errorf("field %q not present", name)
}

func U32(v xdr.ScVal) (uint32, error) {
	if v.Type != xdr.ScValTypeScvU32 || v.U32 == nil {
		return 0, fmt.Errorf("expected u32, got %v", v.Type)
	}
	return uint32(*v.U32), nil
}

func U64(v xdr.ScVal) (uint64, error) {
	if v.Type != xdr.ScValTypeScvU64 || v.U64 == nil {
		return 0, fmt.Errorf("expected u64, got %v", v.Type)
	}
	return uint64(*v.U64), nil
}

// I128 narrows to int64. Tuitio amounts fit comfortably; anything larger is a
// data problem worth failing on rather than silently truncating.
func I128(v xdr.ScVal) (int64, error) {
	if v.Type != xdr.ScValTypeScvI128 || v.I128 == nil {
		return 0, fmt.Errorf("expected i128, got %v", v.Type)
	}
	hi, lo := int64(v.I128.Hi), uint64(v.I128.Lo)
	if hi != 0 && hi != -1 {
		return 0, fmt.Errorf("i128 exceeds int64 range (hi=%d)", hi)
	}
	n := int64(lo)
	if hi == -1 {
		n = -n
	}
	return n, nil
}

func Address(v xdr.ScVal) (string, error) {
	if v.Type != xdr.ScValTypeScvAddress || v.Address == nil {
		return "", fmt.Errorf("expected address, got %v", v.Type)
	}
	return v.Address.String()
}

func String(v xdr.ScVal) (string, error) {
	if v.Type != xdr.ScValTypeScvString || v.Str == nil {
		return "", fmt.Errorf("expected string, got %v", v.Type)
	}
	return string(*v.Str), nil
}

func Bool(v xdr.ScVal) (bool, error) {
	if v.Type != xdr.ScValTypeScvBool || v.B == nil {
		return false, fmt.Errorf("expected bool, got %v", v.Type)
	}
	return *v.B, nil
}

// field helpers that fail fast with the field name in the error.
func fieldU32(v xdr.ScVal, name string) (uint32, error) {
	f, err := Field(v, name)
	if err != nil {
		return 0, err
	}
	n, err := U32(f)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return n, nil
}

func fieldU64(v xdr.ScVal, name string) (uint64, error) {
	f, err := Field(v, name)
	if err != nil {
		return 0, err
	}
	n, err := U64(f)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return n, nil
}

func fieldI128(v xdr.ScVal, name string) (int64, error) {
	f, err := Field(v, name)
	if err != nil {
		return 0, err
	}
	n, err := I128(f)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return n, nil
}

func fieldAddress(v xdr.ScVal, name string) (string, error) {
	f, err := Field(v, name)
	if err != nil {
		return "", err
	}
	s, err := Address(f)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return s, nil
}

func fieldString(v xdr.ScVal, name string) (string, error) {
	f, err := Field(v, name)
	if err != nil {
		return "", err
	}
	s, err := String(f)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return s, nil
}
