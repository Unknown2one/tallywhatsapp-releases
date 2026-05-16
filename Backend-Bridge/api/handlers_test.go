package api

import (
	"math/rand"
	"testing"
	"time"
)

func TestNormalizeVoucherType(t *testing.T) {
	testCases := []struct {
		name  string
		msg   string
		file  string
		input string
		want  string
	}{
		{name: "receipt by explicit type", input: "receipt", want: "receipt"},
		{name: "receipt mixed case type", input: " Receipt ", want: "receipt"},
		{name: "receipt by voucher type substring", input: "receipt book", want: "receipt"},
		{name: "receipt by message keyword", input: "sale", msg: "Your Receipt No. 10 is attached", want: "receipt"},
		{name: "receipt by file name keyword", input: "sale", file: "TallyWhatsapp\\Receipt_10.pdf", want: "receipt"},
		{name: "ledger", input: "ledger", want: "ledger"},
		{name: "sale", input: "sale", want: "sale"},
		{name: "unknown defaults to sale", input: "invoice", want: "sale"},
		{name: "empty defaults to sale", input: "", want: "sale"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeVoucherType(tc.input, tc.msg, tc.file); got != tc.want {
				t.Fatalf("normalizeVoucherType(%q, %q, %q) = %q, want %q", tc.input, tc.msg, tc.file, got, tc.want)
			}
		})
	}
}

func TestNextReceiptDelayRange(t *testing.T) {
	handler := &Handler{
		rng: rand.New(rand.NewSource(1)),
	}

	for i := 0; i < 20; i++ {
		delay := handler.nextReceiptDelay()
		if delay < 2*time.Minute || delay >= 3*time.Minute {
			t.Fatalf("nextReceiptDelay() = %s, want range [2m, 3m)", delay)
		}
	}
}
