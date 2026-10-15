package alerts

import (
	"errors"
	"net/url"
	"strings"
)

// ValidateSlackURL checks the address of a Slack channel. It has to be https: it is a credential, and anyone who can
// read it can post as the channel. Whether the host may be reached is checked separately, when the address is saved.
func ValidateSlackURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("enter the Slack incoming webhook URL, which starts with https://hooks.slack.com/")
	}
	if len(raw) > 2048 {
		return errors.New("the webhook URL must be under 2048 characters")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return errors.New("enter a full URL such as https://hooks.slack.com/services/T000/B000/XXXX")
	}
	if parsed.Scheme != "https" {
		return errors.New("the webhook URL must start with https://")
	}
	if parsed.User != nil {
		return errors.New("the webhook URL cannot include a username or password")
	}
	if parsed.Fragment != "" {
		return errors.New("the webhook URL cannot include a fragment")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return errors.New("the webhook URL cannot point at localhost")
	}
	return nil
}

// SlackHost is the host a Slack address posts to: all of it that is safe to show.
func SlackHost(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
