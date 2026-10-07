package chain

import (
	"context"
	"errors"
)

// GrantState mirrors the escrow contract's Grant struct.
type GrantState struct {
	Sponsor     string
	Beneficiary string
	Institution string
	Token       string
	TermAmount  int64
	TermsTotal  uint32
	NextTerm    uint32
	Status      uint32
	CreatedAt   uint64
}

// TermState mirrors the escrow contract's Term struct.
type TermState struct {
	Status       uint32
	AttestedAt   uint64
	ReleaseAfter uint64
}

// InstitutionState mirrors the registry contract's Institution struct.
type InstitutionState struct {
	Admin        string
	Payout       string
	Name         string
	Country      string
	Status       uint32
	RegisteredAt uint64
}

// Contract enum discriminants, which Soroban encodes as u32.
const (
	GrantActive    uint32 = 0
	GrantCompleted uint32 = 1
	GrantCancelled uint32 = 2

	TermPending  uint32 = 0
	TermAttested uint32 = 1
	TermDisputed uint32 = 2
	TermReleased uint32 = 3
	TermRefunded uint32 = 4

	InstitutionPending   uint32 = 0
	InstitutionVerified  uint32 = 1
	InstitutionSuspended uint32 = 2
)

// GrantStatusName maps a contract discriminant to the read model's label.
func GrantStatusName(s uint32) string {
	switch s {
	case GrantCompleted:
		return "completed"
	case GrantCancelled:
		return "cancelled"
	default:
		return "active"
	}
}

// TermStatusName maps a contract discriminant to the read model's label.
func TermStatusName(s uint32) string {
	switch s {
	case TermAttested:
		return "attested"
	case TermDisputed:
		return "disputed"
	case TermReleased:
		return "released"
	case TermRefunded:
		return "refunded"
	default:
		return "pending"
	}
}

// InstitutionStatusName maps a contract discriminant to the read model's label.
func InstitutionStatusName(s uint32) string {
	switch s {
	case InstitutionVerified:
		return "verified"
	case InstitutionSuspended:
		return "suspended"
	default:
		return "pending"
	}
}

// ErrNotFound reports that the contract holds no such entry.
var ErrNotFound = errors.New("not found in contract state")

// Escrow reads the tuition escrow contract.
type Escrow struct {
	reader   *Reader
	contract string
}

func NewEscrow(r *Reader, contractID string) *Escrow {
	return &Escrow{reader: r, contract: contractID}
}

// NextGrantID is one past the highest grant id the contract has issued.
func (e *Escrow) NextGrantID(ctx context.Context) (uint64, error) {
	v, err := e.reader.Call(ctx, e.contract, "next_grant_id")
	if err != nil {
		return 0, err
	}
	return U64(v)
}

func (e *Escrow) Grant(ctx context.Context, id uint64) (GrantState, error) {
	var g GrantState
	v, err := e.reader.Call(ctx, e.contract, "get_grant", u64Arg(id))
	if err != nil {
		var ce *ContractError
		if errors.As(err, &ce) {
			return g, ErrNotFound
		}
		return g, err
	}
	if g.Sponsor, err = fieldAddress(v, "sponsor"); err != nil {
		return g, err
	}
	if g.Beneficiary, err = fieldAddress(v, "beneficiary"); err != nil {
		return g, err
	}
	if g.Institution, err = fieldAddress(v, "institution"); err != nil {
		return g, err
	}
	if g.Token, err = fieldAddress(v, "token"); err != nil {
		return g, err
	}
	if g.TermAmount, err = fieldI128(v, "term_amount"); err != nil {
		return g, err
	}
	if g.TermsTotal, err = fieldU32(v, "terms_total"); err != nil {
		return g, err
	}
	if g.NextTerm, err = fieldU32(v, "next_term"); err != nil {
		return g, err
	}
	if g.Status, err = fieldU32(v, "status"); err != nil {
		return g, err
	}
	if g.CreatedAt, err = fieldU64(v, "created_at"); err != nil {
		return g, err
	}
	return g, nil
}

func (e *Escrow) Term(ctx context.Context, grantID uint64, index uint32) (TermState, error) {
	var t TermState
	v, err := e.reader.Call(ctx, e.contract, "get_term", u64Arg(grantID), u32Arg(index))
	if err != nil {
		var ce *ContractError
		if errors.As(err, &ce) {
			return t, ErrNotFound
		}
		return t, err
	}
	if t.Status, err = fieldU32(v, "status"); err != nil {
		return t, err
	}
	if t.AttestedAt, err = fieldU64(v, "attested_at"); err != nil {
		return t, err
	}
	if t.ReleaseAfter, err = fieldU64(v, "release_after"); err != nil {
		return t, err
	}
	return t, nil
}

// Registry reads the institution registry contract.
type Registry struct {
	reader   *Reader
	contract string
}

func NewRegistry(r *Reader, contractID string) *Registry {
	return &Registry{reader: r, contract: contractID}
}

func (reg *Registry) Count(ctx context.Context) (uint32, error) {
	v, err := reg.reader.Call(ctx, reg.contract, "count")
	if err != nil {
		return 0, err
	}
	return U32(v)
}

func (reg *Registry) Institution(ctx context.Context, address string) (InstitutionState, error) {
	var i InstitutionState
	arg, err := addressArg(address)
	if err != nil {
		return i, err
	}
	v, err := reg.reader.Call(ctx, reg.contract, "get_institution", arg)
	if err != nil {
		var ce *ContractError
		if errors.As(err, &ce) {
			return i, ErrNotFound
		}
		return i, err
	}
	if i.Admin, err = fieldAddress(v, "admin"); err != nil {
		return i, err
	}
	if i.Payout, err = fieldAddress(v, "payout"); err != nil {
		return i, err
	}
	if i.Name, err = fieldString(v, "name"); err != nil {
		return i, err
	}
	if i.Country, err = fieldString(v, "country"); err != nil {
		return i, err
	}
	if i.Status, err = fieldU32(v, "status"); err != nil {
		return i, err
	}
	if i.RegisteredAt, err = fieldU64(v, "registered_at"); err != nil {
		return i, err
	}
	return i, nil
}
