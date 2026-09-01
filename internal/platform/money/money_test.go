package money

import "testing"

func TestParseDecimal(t *testing.T) {
	ok := map[string]int64{
		"0":        0,
		"0.00":     0,
		"1":        100,
		"100":      10000,
		"100.5":    10050,
		"100.50":   10050,
		"0.01":     1,
		"0.1":      10,
		"12345.99": 1234599,
	}
	for in, want := range ok {
		got, err := ParseDecimal(in)
		if err != nil {
			t.Errorf("ParseDecimal(%q) erro inesperado: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseDecimal(%q) = %d, quero %d", in, got, want)
		}
	}

	bad := []string{"", " ", "-1", "+1", "1.234", "1.", ".5", "abc", "1,50", "1.2.3"}
	for _, in := range bad {
		if _, err := ParseDecimal(in); err == nil {
			t.Errorf("ParseDecimal(%q) deveria falhar", in)
		}
	}
}

func TestFormatCents(t *testing.T) {
	cases := map[int64]string{
		0:       "0.00",
		1:       "0.01",
		10:      "0.10",
		100:     "1.00",
		10050:   "100.50",
		1234599: "12345.99",
		-5:      "-0.05",
	}
	for in, want := range cases {
		if got := FormatCents(in); got != want {
			t.Errorf("FormatCents(%d) = %q, quero %q", in, got, want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, in := range []string{"0.00", "1.00", "100.50", "9999.99"} {
		cents, err := ParseDecimal(in)
		if err != nil {
			t.Fatalf("parse %q: %v", in, err)
		}
		if out := FormatCents(cents); out != in {
			t.Errorf("round-trip %q -> %d -> %q", in, cents, out)
		}
	}
}
