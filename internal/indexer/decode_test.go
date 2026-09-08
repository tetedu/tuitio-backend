package indexer

import "testing"

// Fixtures below are real base64 XDR captured from the deployed contracts on
// Stellar testnet (ledger ~4.57M, 2026-09-08). If the contract event schema
// ever changes, these tests fail loudly rather than silently mis-indexing.

func TestDecodeInstitutionVerified(t *testing.T) {
	const value = "AAAAEQAAAAEAAAABAAAADwAAAAt2ZXJpZmllZF9hdAAAAAAFAAAAAGqgHvs="
	topics := []string{
		"AAAADwAAABRpbnN0aXR1dGlvbl92ZXJpZmllZA==",
		"AAAAEgAAAAAAAAAAZHN8L22XSBWa9C6N5GJxXm7KRuTOeERAvCT1zGGfxkg=",
	}
	got, err := Decode(EventInstitutionVerified, topics, value)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	d, ok := got.(InstitutionVerified)
	if !ok {
		t.Fatalf("wrong type %T", got)
	}
	if d.Institution != "GBSHG7BPNWLUQFM26QXI3ZDCOFPG5SSG4THHQRCAXQSPLTDBT7DEQ7XO" {
		t.Errorf("institution = %q", d.Institution)
	}
	if d.VerifiedAt != 1788878587 {
		t.Errorf("verified_at = %d", d.VerifiedAt)
	}
}

func TestDecodeGrantCreated(t *testing.T) {
	const value = "AAAAEQAAAAEAAAAFAAAADwAAAAtiZW5lZmljaWFyeQAAAAASAAAAAAAAAAAlHzRe2vNbn9Ro9HBSpkJffAv5nM3O7AJI7j2K9N/NiQAAAA8AAAALdGVybV9hbW91bnQAAAAACgAAAAAAAAAAAAAAAB3NZQAAAAAPAAAAC3Rlcm1zX3RvdGFsAAAAAAMAAAADAAAADwAAAAV0b2tlbgAAAAAAABIAAAAB15KLcsJwPM/q9+uf9O9NUEpVqLl5/JtFDqLIQrTRzmEAAAAPAAAADHRvdGFsX2Z1bmRlZAAAAAoAAAAAAAAAAAAAAABZaC8A"
	topics := []string{
		"AAAADwAAAA1ncmFudF9jcmVhdGVkAAAA",
		"AAAABQAAAAAAAAAA",
		"AAAAEgAAAAAAAAAARi26BFroAGaRbnsXOfvi4RDGdd5hlXyUzNmh4qBYZec=",
		"AAAAEgAAAAAAAAAAZHN8L22XSBWa9C6N5GJxXm7KRuTOeERAvCT1zGGfxkg=",
	}
	got, err := Decode(EventGrantCreated, topics, value)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	d, ok := got.(GrantCreated)
	if !ok {
		t.Fatalf("wrong type %T", got)
	}
	if d.GrantID != 0 {
		t.Errorf("grant_id = %d", d.GrantID)
	}
	if d.Sponsor != "GBDC3OQELLUAAZURNZ5ROOP34LQRBRTV3ZQZK7EUZTM2DYVALBS6P3JI" {
		t.Errorf("sponsor = %q", d.Sponsor)
	}
	if d.Institution != "GBSHG7BPNWLUQFM26QXI3ZDCOFPG5SSG4THHQRCAXQSPLTDBT7DEQ7XO" {
		t.Errorf("institution = %q", d.Institution)
	}
	if d.Beneficiary != "GASR6NC63LZVXH6UND2HAUVGIJPXYC7ZTTG453ACJDXD3CXU37GYTMA4" {
		t.Errorf("beneficiary = %q", d.Beneficiary)
	}
	if d.TermAmount != 500000000 {
		t.Errorf("term_amount = %d", d.TermAmount)
	}
	if d.TermsTotal != 3 {
		t.Errorf("terms_total = %d", d.TermsTotal)
	}
	if d.Token != "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC" {
		t.Errorf("token = %q", d.Token)
	}
	if d.TotalFunded != 1500000000 {
		t.Errorf("total_funded = %d", d.TotalFunded)
	}
}

func TestDecodeUnknownEventReturnsNil(t *testing.T) {
	got, err := Decode("something_new", nil, "AAAAEQAAAAEAAAABAAAADwAAAAt2ZXJpZmllZF9hdAAAAAAFAAAAAGqgHvs=")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %T", got)
	}
}

func TestDecodeTermAttested(t *testing.T) {
	const value = "AAAAEQAAAAEAAAADAAAADwAAAAthdHRlc3RlZF9hdAAAAAAFAAAAAGqgH1UAAAAPAAAAC2luc3RpdHV0aW9uAAAAABIAAAAAAAAAAGRzfC9tl0gVmvQujeRicV5uykbkznhEQLwk9cxhn8ZIAAAADwAAAA1yZWxlYXNlX2FmdGVyAAAAAAAABQAAAABqqVnV"
	topics := []string{"AAAADwAAAA10ZXJtX2F0dGVzdGVkAAAA", "AAAABQAAAAAAAAAA", "AAAAAwAAAAA="}
	got, err := Decode(EventTermAttested, topics, value)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	d, ok := got.(TermAttested)
	if !ok {
		t.Fatalf("wrong type %T", got)
	}
	if d.GrantID != 0 || d.TermIndex != 0 {
		t.Errorf("grant/term = %d/%d", d.GrantID, d.TermIndex)
	}
	if d.AttestedAt != 1788878677 {
		t.Errorf("attested_at = %d", d.AttestedAt)
	}
	if d.ReleaseAfter != 1789483477 {
		t.Errorf("release_after = %d", d.ReleaseAfter)
	}
	if d.Institution != "GBSHG7BPNWLUQFM26QXI3ZDCOFPG5SSG4THHQRCAXQSPLTDBT7DEQ7XO" {
		t.Errorf("institution = %q", d.Institution)
	}
}

func TestDecodeDisputeResolved(t *testing.T) {
	const value = "AAAAEQAAAAEAAAACAAAADwAAAAZhbW91bnQAAAAAAAoAAAAAAAAAAAAAAAAdzWUAAAAADwAAAAhyZWxlYXNlZAAAAAAAAAAB"
	topics := []string{"AAAADwAAABBkaXNwdXRlX3Jlc29sdmVk", "AAAABQAAAAAAAAAB", "AAAAAwAAAAA="}
	got, err := Decode(EventDisputeResolved, topics, value)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	d, ok := got.(DisputeResolved)
	if !ok {
		t.Fatalf("wrong type %T", got)
	}
	if !d.Released {
		t.Errorf("released = false")
	}
	if d.Amount != 500000000 {
		t.Errorf("amount = %d", d.Amount)
	}
	if d.GrantID != 1 || d.TermIndex != 0 {
		t.Errorf("grant/term = %d/%d", d.GrantID, d.TermIndex)
	}
}
