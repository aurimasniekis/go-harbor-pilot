// Package money implements exact fixed-point currency amounts.
//
// Amounts are stored as int64 counts of 1e-10 currency units ("e10 units").
// That keeps sub-cent per-token charges exact (USD 0.01 per million tokens is
// 100 e10 units per token) while still covering roughly ±922 million currency
// units, and lets SQLite SUM() aggregate exactly. Floating point is never used
// for financial aggregation.
package money

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
)

// Scale is the number of decimal places represented by one unit.
const Scale = 10

// unitsPerWhole is 10^Scale.
const unitsPerWhole int64 = 10_000_000_000

// ErrOverflow is returned when an operation exceeds the representable range.
var ErrOverflow = errors.New("money: amount overflow")

// Amount is an exact amount in 1e-10 currency units. Currency is tracked
// separately (see Money) because amounts in different currencies must never be
// added together.
type Amount int64

// Money pairs an Amount with an ISO 4217-style currency code (upper case).
type Money struct {
	Amount   Amount `json:"amount_e10"`
	Currency string `json:"currency"`
}

// ParseDecimal parses a base-10 decimal string such as "12.34", "-0.000001"
// or "1e-3" exactly. Digits beyond Scale decimals are rounded half-to-even.
func ParseDecimal(s string) (Amount, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("money: empty decimal")
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, fmt.Errorf("money: invalid decimal %q", s)
	}
	return fromRat(r)
}

// FromMinorUnits converts an integer count of minor units at the given
// exponent (e.g. cents => exp 2) into an Amount.
func FromMinorUnits(v int64, exp int) (Amount, error) {
	if exp < 0 || exp > Scale {
		return 0, fmt.Errorf("money: unsupported exponent %d", exp)
	}
	mul := pow10(Scale - exp)
	if v != 0 && (v > math.MaxInt64/mul || v < math.MinInt64/mul) {
		return 0, ErrOverflow
	}
	return Amount(v * mul), nil
}

// ParseScaledDecimal parses a decimal string expressed in 10^-exp units, e.g.
// Anthropic cost reports give "123.45" meaning 123.45 cents (exp=2).
func ParseScaledDecimal(s string, exp int) (Amount, error) {
	r, ok := new(big.Rat).SetString(strings.TrimSpace(s))
	if !ok {
		return 0, fmt.Errorf("money: invalid decimal %q", s)
	}
	r.Quo(r, new(big.Rat).SetInt64(pow10(exp)))
	return fromRat(r)
}

func fromRat(r *big.Rat) (Amount, error) {
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt64(unitsPerWhole))
	q, rem := new(big.Int).QuoRem(scaled.Num(), scaled.Denom(), new(big.Int))
	// Round half to even.
	if rem.Sign() != 0 {
		twice := new(big.Int).Mul(new(big.Int).Abs(rem), big.NewInt(2))
		cmp := twice.Cmp(scaled.Denom())
		if cmp > 0 || (cmp == 0 && q.Bit(0) == 1) {
			if scaled.Sign() < 0 {
				q.Sub(q, big.NewInt(1))
			} else {
				q.Add(q, big.NewInt(1))
			}
		}
	}
	if !q.IsInt64() {
		return 0, ErrOverflow
	}
	return Amount(q.Int64()), nil
}

// Add returns a+b or ErrOverflow.
func Add(a, b Amount) (Amount, error) {
	s := a + b
	if (b > 0 && s < a) || (b < 0 && s > a) {
		return 0, ErrOverflow
	}
	return s, nil
}

// Sum adds amounts exactly.
func Sum(xs ...Amount) (Amount, error) {
	var total Amount
	for _, x := range xs {
		var err error
		total, err = Add(total, x)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

// CostForTokens computes tokens × pricePerMillion / 1e6 exactly, rounding
// half-to-even to e10 units. pricePerMillion is the price of one million
// units of the billable quantity.
func CostForTokens(tokens int64, pricePerMillion Amount) (Amount, error) {
	n := new(big.Int).Mul(big.NewInt(tokens), big.NewInt(int64(pricePerMillion)))
	// n is in e10 units × 1e6; convert back to whole currency for fromRat.
	return fromRat(new(big.Rat).SetFrac(n, big.NewInt(1_000_000*unitsPerWhole)))
}

// String formats the amount with the minimal number of decimals (at least 2).
func (a Amount) String() string {
	return a.Format(-1)
}

// Format renders the amount with the given number of decimals (rounded
// half-to-even); decimals < 0 means "trim trailing zeros, keep at least 2".
func (a Amount) Format(decimals int) string {
	neg := a < 0
	v := new(big.Int).SetInt64(int64(a))
	if neg {
		v.Neg(v)
	}
	if decimals >= 0 && decimals < Scale {
		div := big.NewInt(pow10(Scale - decimals))
		q, rem := new(big.Int).QuoRem(v, div, new(big.Int))
		twice := new(big.Int).Mul(rem, big.NewInt(2))
		if c := twice.Cmp(div); c > 0 || (c == 0 && q.Bit(0) == 1) {
			q.Add(q, big.NewInt(1))
		}
		v = q.Mul(q, div)
	}
	whole := new(big.Int).Quo(v, big.NewInt(unitsPerWhole))
	frac := new(big.Int).Rem(v, big.NewInt(unitsPerWhole))
	fs := fmt.Sprintf("%010s", frac.String())
	switch {
	case decimals < 0:
		fs = strings.TrimRight(fs, "0")
		for len(fs) < 2 {
			fs += "0"
		}
	case decimals > Scale:
		fs += strings.Repeat("0", decimals-Scale)
	default:
		fs = fs[:decimals]
	}
	sign := ""
	if neg && (whole.Sign() != 0 || strings.Trim(fs, "0") != "") {
		sign = "-"
	}
	if fs == "" {
		return sign + whole.String()
	}
	return sign + whole.String() + "." + fs
}

// Float64 converts to float64 for display/charting only. Never aggregate the
// result.
func (a Amount) Float64() float64 {
	return float64(a) / float64(unitsPerWhole)
}

func pow10(n int) int64 {
	v := int64(1)
	for range n {
		v *= 10
	}
	return v
}
