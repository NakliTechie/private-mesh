package cmd

import "testing"

func TestCaveatFromRateSpec(t *testing.T) {
	cases := []struct {
		in, want string
		err      bool
	}{
		{"1000/hour", "rate <= 1000 per hour", false},
		{"10/minute", "rate <= 10 per minute", false},
		{"bogus", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := caveatFromRateSpec(c.in)
		if (err != nil) != c.err {
			t.Errorf("%q: err=%v want err=%v", c.in, err, c.err)
		}
		if got != c.want {
			t.Errorf("%q: got %q want %q", c.in, got, c.want)
		}
	}
}

func TestCaveatFromAmountSpec(t *testing.T) {
	cases := []struct {
		in, want string
		err      bool
	}{
		{"1000USD", "max-amount <= 1000 USD", false},
		{"50 EUR", "max-amount <= 50 EUR", false},
		{"USD100", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := caveatFromAmountSpec(c.in)
		if (err != nil) != c.err {
			t.Errorf("%q: err=%v want err=%v", c.in, err, c.err)
		}
		if got != c.want {
			t.Errorf("%q: got %q want %q", c.in, got, c.want)
		}
	}
}
