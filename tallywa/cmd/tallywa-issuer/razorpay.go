package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// VerifyRazorpaySignature checks Razorpay's webhook HMAC.
//
// Per Razorpay's webhook docs, the signature header is:
//
//	X-Razorpay-Signature: hex(hmac_sha256(webhook_secret, raw_body))
//
// The raw request body MUST be used — JSON re-encoding will not match.
func VerifyRazorpaySignature(secret, body []byte, headerSignature string) bool {
	if len(secret) == 0 || headerSignature == "" {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(headerSignature))
}

// PlanSpec maps a plan name (from Razorpay notes) onto edition + features
// + redemption count. Centralized so we can edit pricing in one place
// without touching the redemption handlers.
type PlanSpec struct {
	Edition        string
	Features       []string
	MaxRedemptions int // total lifetime activations allowed
}

// Plans is the shipping price-list. New plans get added here.
//
// Why so many redemptions? A "1 PC" license still needs to allow for the
// customer reformatting, replacing a dead motherboard, or switching
// laptops once. Three is generous without being abusable.
var Plans = map[string]PlanSpec{
	"lite": {
		Edition:        "lite",
		Features:       []string{"sales", "receipt"},
		MaxRedemptions: 3,
	},
	"pro": {
		Edition:        "pro",
		Features:       []string{"sales", "receipt", "ledger", "multi_co"},
		MaxRedemptions: 3,
	},
	"business": {
		Edition:        "business",
		Features:       []string{"sales", "receipt", "ledger", "multi_co", "print"},
		MaxRedemptions: 6,
	},
}
