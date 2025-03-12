package sip

import "errors"

const MagicCookie = "z9hG4bK"

var ErrMismatchedTransaction = errors.New("mismatched transaction")
