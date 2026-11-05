package money

import (
	"errors"
	"math"
	"testing"
)

func TestParseDecimal(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Amount
		wantErr bool
	}{
		{name: "whole", in: "12", want: 120_000_000_000},
		{name: "fraction", in: "0.01", want: 100_000_000},
		{name: "tiny", in: "0.0000000001", want: 1},
		{name: "negative", in: "-1.5", want: -15_000_000_000},
		{name: "exponent", in: "1e-3", want: 10_000_000},
		{name: "round half even down", in: "0.00000000005", want: 0},
		{name: "round half even up", in: "0.00000000015", want: 2},
		{name: "empty", in: "", wantErr: true},
		{name: "garbage", in: "1,5", wantErr: true},
		{name: "overflow", in: "99999999999999999999", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDecimal(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %d want %d", got, tt.want)
			}
		})
	}
}

func TestParseScaledDecimalCents(t *testing.T) {
	// Anthropic cost_report: "123.45" means 123.45 cents = USD 1.2345.
	got, err := ParseScaledDecimal("123.45", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "1.2345" {
		t.Fatalf("got %s", got)
	}
}

func TestSumIsExactWhereFloatIsNot(t *testing.T) {
	tenCents, _ := ParseDecimal("0.1")
	var parts []Amount
	for range 10 {
		parts = append(parts, tenCents)
	}
	total, err := Sum(parts...)
	if err != nil {
		t.Fatal(err)
	}
	if total.String() != "1.00" {
		t.Fatalf("got %s", total)
	}
	f := 0.0
	for range 10 {
		f += 0.1
	}
	if f == 1.0 {
		t.Skip("float happened to be exact on this platform")
	}
}

func TestAddOverflow(t *testing.T) {
	if _, err := Add(Amount(math.MaxInt64), 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("expected overflow, got %v", err)
	}
	if _, err := Add(Amount(math.MinInt64), -1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("expected overflow, got %v", err)
	}
}

func TestCostForTokens(t *testing.T) {
	tests := []struct {
		name   string
		tokens int64
		price  string // per million
		want   string
	}{
		{name: "one million at 3 USD", tokens: 1_000_000, price: "3", want: "3.00"},
		{name: "single token at 0.01 per million", tokens: 1, price: "0.01", want: "0.00000001"},
		{name: "1234 tokens at 15", tokens: 1234, price: "15", want: "0.01851"},
		{name: "zero tokens", tokens: 0, price: "15", want: "0.00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ParseDecimal(tt.price)
			if err != nil {
				t.Fatal(err)
			}
			got, err := CostForTokens(tt.tokens, p)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.want {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
}

func TestFormat(t *testing.T) {
	a, _ := ParseDecimal("14.205")
	if got := a.Format(2); got != "14.20" {
		t.Fatalf("half-even format: got %s", got)
	}
	b, _ := ParseDecimal("-0.004")
	if got := b.Format(2); got != "0.00" {
		t.Fatalf("negative zero: got %s", got)
	}
	c, _ := ParseDecimal("3")
	if got := c.Format(0); got != "3" {
		t.Fatalf("zero decimals: got %s", got)
	}
}
