package githubapp

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const forwardSecret = "forwarding-secret"

// The signature a provider has to produce, pinned to bytes computed outside Go (openssl dgst -sha256 -hmac), so that a
// provider written in another language can check its own against the same value.
func TestAForwardedSignatureIsTheHMACOfTheTimeTheEventAndTheBody(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	require.Equal(t, "t=1761213600,v1=2c37dd08ada1978d93fb1b0e6466e4036c7244d1e1c36e8ed60e17e424194a5c",
		SignForwarded(forwardSecret, 1761213600, "push", body))
}

func TestAForwardedEventSignedWithTheClustersSecretIsAccepted(t *testing.T) {
	now := time.Unix(1761213600, 0)
	body := []byte(`{"ref":"refs/heads/main"}`)
	header := SignForwarded(forwardSecret, now.Unix(), "push", body)

	require.NoError(t, VerifyForwarded(forwardSecret, header, "push", body, now))
	require.NoError(t, VerifyForwarded(forwardSecret, header, "push", body, now.Add(ForwardTolerance)), "at the edge of the tolerance")
	require.NoError(t, VerifyForwarded(forwardSecret, header, "push", body, now.Add(-ForwardTolerance)), "a clock that is a little behind")
}

func TestAForwardedEventIsRefusedForAnythingThatDoesNotMatch(t *testing.T) {
	now := time.Unix(1761213600, 0)
	body := []byte(`{"ref":"refs/heads/main"}`)
	good := SignForwarded(forwardSecret, now.Unix(), "push", body)
	for name, tc := range map[string]struct {
		secret, header, event string
		body                  []byte
		want                  error
	}{
		"no signature":                 {forwardSecret, "", "push", body, ErrForwardSignatureMissing},
		"only spaces":                  {forwardSecret, "   ", "push", body, ErrForwardSignatureMissing},
		"another secret":               {"another-secret", good, "push", body, ErrForwardSignatureInvalid},
		"no secret on the cluster":     {"", good, "push", body, ErrForwardSignatureInvalid},
		"a changed body":               {forwardSecret, good, "push", []byte(`{"ref":"refs/heads/other"}`), ErrForwardSignatureInvalid},
		"a body with a byte more":      {forwardSecret, good, "push", append(append([]byte{}, body...), '\n'), ErrForwardSignatureInvalid},
		"another kind of event":        {forwardSecret, good, "pull_request", body, ErrForwardSignatureInvalid},
		"a time that was moved":        {forwardSecret, strings.Replace(good, "t=1761213600", "t=1761213601", 1), "push", body, ErrForwardSignatureInvalid},
		"no time":                      {forwardSecret, good[strings.Index(good, "v1="):], "push", body, ErrForwardSignatureInvalid},
		"no v1":                        {forwardSecret, "t=1761213600", "push", body, ErrForwardSignatureInvalid},
		"a v1 that is not hex":         {forwardSecret, "t=1761213600,v1=zz", "push", body, ErrForwardSignatureInvalid},
		"a v1 that is too short":       {forwardSecret, "t=1761213600,v1=abcd", "push", body, ErrForwardSignatureInvalid},
		"two times":                    {forwardSecret, "t=1761213600," + good, "push", body, ErrForwardSignatureInvalid},
		"not key=value":                {forwardSecret, "sha256=abc", "push", body, ErrForwardSignatureInvalid},
		"GitHub's own signature shape": {forwardSecret, "sha256" + good[strings.Index(good, "v1=")+2:], "push", body, ErrForwardSignatureInvalid},
	} {
		require.ErrorIs(t, VerifyForwarded(tc.secret, tc.header, tc.event, tc.body, now), tc.want, name)
	}
}

func TestAForwardedEventOlderThanTheToleranceIsRefusedSoACopyCannotBeSentLater(t *testing.T) {
	signedAt := time.Unix(1761213600, 0)
	body := []byte(`{"ref":"refs/heads/main"}`)
	header := SignForwarded(forwardSecret, signedAt.Unix(), "push", body)

	require.ErrorIs(t, VerifyForwarded(forwardSecret, header, "push", body, signedAt.Add(ForwardTolerance+time.Second)), ErrForwardSignatureStale)
	require.ErrorIs(t, VerifyForwarded(forwardSecret, header, "push", body, signedAt.Add(-ForwardTolerance-time.Second)), ErrForwardSignatureStale,
		"a time far in the future is refused too: it would make a copy good for longer")
}

func TestAStaleTimeIsOnlyReportedForASignatureThatMatches(t *testing.T) {
	signedAt := time.Unix(1761213600, 0)
	body := []byte(`{}`)
	forged := SignForwarded("not-the-secret", signedAt.Unix(), "push", body)

	require.ErrorIs(t, VerifyForwarded(forwardSecret, forged, "push", body, signedAt.Add(time.Hour)), ErrForwardSignatureInvalid,
		"a sender without the secret learns nothing about the cluster's clock")
}

func TestASignatureWithTheOldAndTheNewSecretIsAcceptedWhileAProviderChangesIt(t *testing.T) {
	now := time.Unix(1761213600, 0)
	body := []byte(`{}`)
	old := SignForwarded("old-secret", now.Unix(), "push", body)
	current := SignForwarded(forwardSecret, now.Unix(), "push", body)
	both := old + "," + current[strings.Index(current, "v1="):]

	require.NoError(t, VerifyForwarded(forwardSecret, both, "push", body, now))
	require.NoError(t, VerifyForwarded("old-secret", both, "push", body, now))

	many := both + strings.Repeat(","+current[strings.Index(current, "v1="):], maxForwardSignatures)
	require.ErrorIs(t, VerifyForwarded(forwardSecret, many, "push", body, now), ErrForwardSignatureInvalid, "the number of values is bounded")
}
