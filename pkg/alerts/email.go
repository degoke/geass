package alerts

import (
	"fmt"
	"net/mail"
	"strings"
)

// ParseAddress reads one address a person typed, as "alerts@example.com" or "Geass <alerts@example.com>".
//
// Alertmanager reads an address as a template, so one with braces in it is refused: a quoted address can legally
// contain them, and a person has no reason to need one.
func ParseAddress(text string) (*mail.Address, error) {
	text = strings.TrimSpace(text)
	if strings.ContainsAny(text, "\r\n{}") {
		return nil, fmt.Errorf("%q is not an email address", text)
	}
	address, err := mail.ParseAddress(text)
	if err != nil {
		return nil, fmt.Errorf("%q is not an email address", text)
	}
	return address, nil
}

// ParseRecipients reads the addresses of an email channel. They are separated by commas, spaces or lines, and a
// repeat is dropped.
func ParseRecipients(text string) ([]string, error) {
	var recipients []string
	seen := map[string]bool{}
	for _, item := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\r' || r == '\t' }) {
		address, err := ParseAddress(item)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(address.Address)
		if seen[key] {
			continue
		}
		seen[key] = true
		recipients = append(recipients, address.Address)
	}
	if len(recipients) == 0 {
		return nil, fmt.Errorf("enter at least one email address")
	}
	if len(recipients) > MaxRecipients {
		return nil, fmt.Errorf("an email channel can write to at most %d addresses", MaxRecipients)
	}
	return recipients, nil
}
