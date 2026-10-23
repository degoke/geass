package githubapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// A provider that runs the GitHub App receives GitHub's events itself and sends each one on to the cluster whose
// installation it is. GitHub's own signature is made with the App's webhook secret, which the cluster does not hold, so
// the provider signs what it forwards with a secret it shares with this cluster alone, the way Geass signs the webhooks
// it sends itself: X-Geass-Signature is "t=<unix seconds>,v1=<hex HMAC-SHA256>". What is signed is
// "<t>.<X-GitHub-Event>.<body>", the body exactly as GitHub sent it, so neither the time nor the kind of event can be
// changed without the signature failing.

const (
	// ForwardSignatureHeader carries the provider's signature of a forwarded event.
	ForwardSignatureHeader = "X-Geass-Signature"
	// ForwardTolerance is how far the time in a signature may be from the cluster's clock. A forwarded event older than
	// that is refused, so a copy of one cannot be sent again later.
	ForwardTolerance = 5 * time.Minute
	// maxForwardSignatures bounds the v1 values a signature may carry: more than one is a provider changing its secret.
	maxForwardSignatures = 4
)

// Why a forwarded event's signature was refused. The answer to the sender says which, and never anything of the secret.
var (
	ErrForwardSignatureMissing = errors.New("the forwarded event is not signed")
	ErrForwardSignatureInvalid = errors.New("the forwarded event's signature does not match")
	ErrForwardSignatureStale   = errors.New("the forwarded event's signature is too old or too far in the future")
)

// SignForwarded is the X-Geass-Signature of an event forwarded to a cluster.
func SignForwarded(secret string, timestamp int64, event string, body []byte) string {
	return fmt.Sprintf("t=%d,v1=%s", timestamp, hex.EncodeToString(forwardMAC(secret, timestamp, event, body)))
}

func forwardMAC(secret string, timestamp int64, event string, body []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.%s.", timestamp, event)
	_, _ = mac.Write(body)
	return mac.Sum(nil)
}

// VerifyForwarded checks a forwarded event's signature against the cluster's secret, at the time now. Every v1 value is
// compared in constant time.
func VerifyForwarded(secret, header, event string, body []byte, now time.Time) error {
	header = strings.TrimSpace(header)
	if header == "" {
		return ErrForwardSignatureMissing
	}
	if secret == "" {
		return ErrForwardSignatureInvalid
	}
	var timestamp int64
	var haveTimestamp bool
	var signatures [][]byte
	for part := range strings.SplitSeq(header, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return ErrForwardSignatureInvalid
		}
		switch key {
		case "t":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil || haveTimestamp {
				return ErrForwardSignatureInvalid
			}
			timestamp, haveTimestamp = parsed, true
		case "v1":
			decoded, err := hex.DecodeString(value)
			if err != nil || len(decoded) != sha256.Size {
				return ErrForwardSignatureInvalid
			}
			signatures = append(signatures, decoded)
		}
	}
	if !haveTimestamp || len(signatures) == 0 || len(signatures) > maxForwardSignatures {
		return ErrForwardSignatureInvalid
	}
	expected := forwardMAC(secret, timestamp, event, body)
	matched := false
	for _, signature := range signatures {
		// Every value is compared, so the time taken does not say which one matched.
		if hmac.Equal(expected, signature) {
			matched = true
		}
	}
	if !matched {
		return ErrForwardSignatureInvalid
	}
	// The time is judged only once the signature is known to be the provider's: before that it is anyone's number.
	skew := now.Sub(time.Unix(timestamp, 0))
	if skew > ForwardTolerance || skew < -ForwardTolerance {
		return ErrForwardSignatureStale
	}
	return nil
}
