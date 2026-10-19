package willc

import "testing"

func TestQuoteRoundTrip(t *testing.T) {
	cases := []string{
		"",
		"plain",
		"a\nb",
		"tab\there",
		"cr\rhere",
		"quote\"here",
		`back\slash`,
		"nul\x00byte",
		"\x7f",
		"\x80\xff",
		"both'quotes\"",
	}
	for _, s := range cases {
		q := Quote(s)
		got, ok := Unquote(q)
		if !ok {
			t.Fatalf("Unquote(%s) failed", q)
		}
		if got != s {
			t.Errorf("round trip = %q, want %q (quoted %s)", got, s, q)
		}
	}
}

func TestQuoteIsASCII(t *testing.T) {
	q := Quote("\xc3\xa9")
	for i := 0; i < len(q); i++ {
		if q[i] < 0x20 || q[i] > 0x7E {
			t.Fatalf("quote %s is not ASCII at byte %d", q, i)
		}
	}
}

func TestUnquoteRejectsBadForms(t *testing.T) {
	cases := []string{
		`abc`,
		`"abc`,
		`"a\qb"`,
		`"\u0041"`,
		`"\u{}"`,
		`"\u{110000}"`,
		`"a` + "\x80" + `b"`,
	}
	for _, s := range cases {
		if _, ok := Unquote(s); ok {
			t.Errorf("Unquote(%q) succeeded, want failure", s)
		}
	}
}
