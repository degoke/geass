package oci

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// sign adds the Date and Authorization headers Oracle wants, following
// https://docs.oracle.com/en-us/iaas/Content/API/Concepts/signingrequests.htm:
//
//   - A GET or DELETE signs (request-target), host and date.
//   - A POST, PUT or PATCH also signs x-content-sha256, content-type and content-length, even when the body is empty.
//   - The headers are signed in the order the headers parameter lists them, which is the order of the documentation's
//     own examples.
//   - The request target is the path and the query exactly as they are sent, so the query must be written the way it is
//     signed (see query).
func (p *Provider) sign(req *http.Request, body []byte) error {
	// A request that carries a Date is signed as it is; the shared client never sets one, so every real request gets
	// the current time, again on each retry.
	date := req.Header.Get("Date")
	if date == "" {
		date = p.now().UTC().Format(http.TimeFormat)
		req.Header.Set("Date", date)
	}
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	names := []string{"date", "(request-target)", "host"}
	lines := []string{
		"date: " + date,
		"(request-target): " + strings.ToLower(req.Method) + " " + req.URL.RequestURI(),
		"host: " + host,
	}
	switch req.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
		sum := sha256.Sum256(body)
		contentSHA := base64.StdEncoding.EncodeToString(sum[:])
		req.Header.Set("x-content-sha256", contentSHA)
		names = append(names, "content-length", "content-type", "x-content-sha256")
		lines = append(lines,
			"content-length: "+strconv.Itoa(len(body)),
			"content-type: "+req.Header.Get("Content-Type"),
			"x-content-sha256: "+contentSHA,
		)
	}
	digest := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		return fmt.Errorf("signing the request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf(`Signature version="1",headers="%s",keyId="%s/%s/%s",algorithm="rsa-sha256",signature="%s"`,
		strings.Join(names, " "), p.tenancy, p.user, p.fingerprint, base64.StdEncoding.EncodeToString(signature)))
	return nil
}
