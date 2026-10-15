package alerts

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSlackURLMustBeHTTPSAndNotLocal(t *testing.T) {
	require.NoError(t, ValidateSlackURL("https://hooks.slack.com/services/T000/B000/XXXX"))
	require.NoError(t, ValidateSlackURL("https://chat.example.com/hooks/abc"))
	for _, bad := range []string{
		"", "http://hooks.slack.com/services/x", "hooks.slack.com/services/x", "https://user:pw@hooks.slack.com/x",
		"https://localhost/hook", "https://x.localhost/hook", "https://hooks.slack.com/x#frag", "https://" + strings.Repeat("a", 2100),
	} {
		require.Error(t, ValidateSlackURL(bad), bad)
	}
	require.Equal(t, "hooks.slack.com", SlackHost("https://hooks.slack.com/services/T000/B000/SECRET"), "the host is all that is shown")
	require.Empty(t, SlackHost("::not a url"))
}

func TestRecipientsAreParsedAndDeduplicated(t *testing.T) {
	got, err := ParseRecipients("Ops@example.com, dev@example.com;ops@example.com\nqa@example.com")
	require.NoError(t, err)
	require.Equal(t, []string{"Ops@example.com", "dev@example.com", "qa@example.com"}, got)

	_, err = ParseRecipients("")
	require.Error(t, err)
	_, err = ParseRecipients("ops@example.com, nonsense")
	require.ErrorContains(t, err, "nonsense")

	many := make([]string, 0, MaxRecipients+1)
	for i := range MaxRecipients + 1 {
		many = append(many, fmt.Sprintf("u%d@example.com", i))
	}
	_, err = ParseRecipients(strings.Join(many, ","))
	require.Error(t, err)
}

func TestAnAddressCannotInjectAHeaderOrATemplate(t *testing.T) {
	// Alertmanager reads an address as a template, and a quoted address may legally hold braces.
	for _, bad := range []string{
		"ops@example.com\r\nBcc: attacker@example.com",
		`"{{ .ExternalURL }}"@example.com`,
		"Geass {{x}} <alerts@example.com>",
	} {
		_, err := ParseAddress(bad)
		require.Error(t, err, bad)
	}
	address, err := ParseAddress("Geass <alerts@example.com>")
	require.NoError(t, err)
	require.Equal(t, "alerts@example.com", address.Address)
}

func TestEveryKindHasAnIDAndALabel(t *testing.T) {
	seen := map[string]bool{}
	for _, kind := range Kinds {
		require.NotEmpty(t, kind.Label)
		require.NotEmpty(t, kind.Description)
		require.False(t, seen[kind.ID], "kind %s is listed twice", kind.ID)
		seen[kind.ID] = true
		require.True(t, ValidKind(kind.ID))
		require.Equal(t, kind.Label, KindLabel(kind.ID))
	}
	require.False(t, ValidKind("disk.full"))
	require.False(t, ValidKind(KindTest), "a test is not something a channel subscribes to")
	require.Equal(t, "disk.full", KindLabel("disk.full"))
}
