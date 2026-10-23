package store

import (
	"testing"
	"time"
)

func TestParsePageLimit(t *testing.T) {
	cases := []struct {
		raw     string
		want    int
		wantErr bool
	}{
		{"", DefaultPageLimit, false},
		{"50", 50, false},
		{"1", 1, false},
		{"200", 200, false},
		{"0", 1, false},     // clamped up
		{"-3", 1, false},    // clamped up
		{"201", 200, false}, // clamped down
		{"abc", 0, true},    // non-integer rejected
	}
	for _, c := range cases {
		got, err := ParsePageLimit(c.raw)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParsePageLimit(%q): expected error", c.raw)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParsePageLimit(%q): unexpected error %v", c.raw, err)
		}
		if got != c.want {
			t.Errorf("ParsePageLimit(%q) = %d, want %d", c.raw, got, c.want)
		}
	}
}

func TestInstitutionCursorRoundTrip(t *testing.T) {
	in := Institution{RegisteredAt: 12345, Address: "GABCDEF"}
	c := encodeInstitutionCursor(in)
	reg, addr, err := DecodeInstitutionCursor(c)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if reg != 12345 || addr != "GABCDEF" {
		t.Errorf("round trip = %d,%q", reg, addr)
	}
	if _, _, err := DecodeInstitutionCursor("garbage"); err == nil {
		t.Error("expected error for malformed cursor")
	}
	if _, _, err := DecodeInstitutionCursor("12."); err == nil {
		t.Error("expected error for under-populated cursor")
	}
}

func TestGrantCursorRoundTrip(t *testing.T) {
	c := encodeCursorInt(77)
	got, err := DecodeGrantCursor(c)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != 77 {
		t.Errorf("round trip = %d", got)
	}
	if _, err := DecodeGrantCursor("nope"); err == nil {
		t.Error("expected error for non-integer cursor")
	}
}

func TestActivityCursorRoundTrip(t *testing.T) {
	in := Activity{Ledger: 999, SeenAt: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
	c := encodeActivityCursor(in)
	ledger, seen, err := DecodeActivityCursor(c)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ledger != 999 || !seen.Equal(in.SeenAt) {
		t.Errorf("round trip = %d,%v", ledger, seen)
	}
	if _, _, err := DecodeActivityCursor("bad|cursor"); err == nil {
		t.Error("expected error for malformed activity cursor")
	}
}
