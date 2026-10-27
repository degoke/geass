package platform

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"time"
)

// The relay requires TLS (STARTTLS) before a client may sign in, with a certificate Geass makes itself, so that it needs
// neither cert-manager nor a public name. Geass keeps an authority of its own for it (geass-mail-relay-ca, ten years) and
// signs the relay's certificate with it (geass-mail-relay-tls, 90 days, for the relay's Service names). The certificate is
// replaced 30 days before it runs out, and the relay picks the new one up from its volume without a restart; clients trust
// the authority, which every organization's geass-mail holds as SMTP_CA_CERT, so a new certificate needs nothing of them.
// The authority is replaced only when it is within a year of running out or is lost, and then every client has to be given
// the new one (a restart of the services that read geass-mail).

const (
	mailRelayCAValidity    = 10 * 365 * 24 * time.Hour
	mailRelayCARenewBefore = 365 * 24 * time.Hour
	mailRelayCertValidity  = 90 * 24 * time.Hour
	mailRelayCertRenew     = 30 * 24 * time.Hour
	// mailRelayClockSkew backdates what is made, so that a machine whose clock is a little behind still takes it.
	mailRelayClockSkew = 5 * time.Minute
)

// NewMailRelayCA makes the authority the relay's certificates are signed with.
func NewMailRelayCA(now time.Time) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := certificateSerial()
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Geass mail relay CA", Organization: []string{"Geass"}},
		NotBefore:             now.Add(-mailRelayClockSkew),
		NotAfter:              now.Add(mailRelayCAValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	return encodeCertificate(der, key)
}

// IssueMailRelayCertificate signs a certificate for the relay's Service names with the authority.
func IssueMailRelayCertificate(caCertPEM, caKeyPEM []byte, now time.Time) (certPEM, keyPEM []byte, err error) {
	ca, err := tls.X509KeyPair(caCertPEM, caKeyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("the mail relay's authority cannot be read: %w", err)
	}
	caCert, err := x509.ParseCertificate(ca.Certificate[0])
	if err != nil {
		return nil, nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := certificateSerial()
	if err != nil {
		return nil, nil, err
	}
	notAfter := now.Add(mailRelayCertValidity)
	if notAfter.After(caCert.NotAfter) {
		notAfter = caCert.NotAfter
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: MailRelayHost(), Organization: []string{"Geass"}},
		DNSNames:     MailRelayDNSNames(),
		NotBefore:    now.Add(-mailRelayClockSkew),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, ca.PrivateKey)
	if err != nil {
		return nil, nil, err
	}
	return encodeCertificate(der, key)
}

// MailRelayCAUsable reports whether an authority can go on signing, and when it has to be replaced. It cannot when it does
// not parse, its key is not its certificate's, it is not an authority, or it is within a year of running out.
func MailRelayCAUsable(certPEM, keyPEM []byte, now time.Time) (time.Time, bool) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return time.Time{}, false
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || !cert.IsCA {
		return time.Time{}, false
	}
	renewAt := cert.NotAfter.Add(-mailRelayCARenewBefore)
	return renewAt, now.Before(renewAt)
}

// MailRelayCertificateUsable reports whether the relay's certificate can go on being served, and when it has to be
// replaced. It cannot when it does not parse, its key is not its, the authority did not sign it, it is not valid for every
// name of the relay, or it is within 30 days of running out.
func MailRelayCertificateUsable(certPEM, keyPEM, caCertPEM []byte, now time.Time) (time.Time, bool) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return time.Time{}, false
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return time.Time{}, false
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caCertPEM) {
		return time.Time{}, false
	}
	for _, name := range MailRelayDNSNames() {
		if _, err := cert.Verify(x509.VerifyOptions{DNSName: name, Roots: roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			return time.Time{}, false
		}
	}
	if !slices.Equal(cert.DNSNames, MailRelayDNSNames()) {
		return time.Time{}, false
	}
	renewAt := cert.NotAfter.Add(-mailRelayCertRenew)
	return renewAt, now.Before(renewAt)
}

func certificateSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

func encodeCertificate(der []byte, key *ecdsa.PrivateKey) (certPEM, keyPEM []byte, err error) {
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if certPEM == nil || keyPEM == nil {
		return nil, nil, errors.New("could not encode the certificate")
	}
	return certPEM, keyPEM, nil
}
