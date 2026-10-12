package installer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestTheAcmeEmailIsReadFromTheEnvironment(t *testing.T) {
	t.Setenv("GEASS_ACME_EMAIL", "  admin@acme.dev \n")
	require.Equal(t, "admin@acme.dev", ACMEEmailFromEnv())

	t.Setenv("GEASS_ACME_EMAIL", "")
	require.Empty(t, ACMEEmailFromEnv(), "unset means no issuer, not a made-up address")
}

func TestAnAcmeEmailIsCheckedBeforeAnythingIsInstalled(t *testing.T) {
	require.NoError(t, ValidateACMEEmail(""), "no email is allowed: the cluster just has no issuer")
	require.NoError(t, ValidateACMEEmail("admin@acme.dev"))
	require.NoError(t, ValidateACMEEmail("first.last+geass@mail.acme.dev"))

	for _, bad := range []string{"admin", "admin@acme", "@acme.dev", "admin@", "ad min@acme.dev", "a@b@acme.dev", "Admin <admin@acme.dev>", strings.Repeat("a", 250) + "@acme.dev"} {
		err := ValidateACMEEmail(bad)
		require.Error(t, err, bad)
		require.Contains(t, err.Error(), "GEASS_ACME_EMAIL", "the error names the setting that has to change")
	}
}

func TestTheClusterIsGivenTheAcmeEmailSoTheOperatorCanCreateTheIssuer(t *testing.T) {
	manifest := geassClusterManifest("default", "v1", "https://127.0.0.1:6443", Network{}, "admin@acme.dev")
	require.Contains(t, manifest, "    certManager:\n      acmeEmail: \"admin@acme.dev\"\n")

	var parsed struct {
		Spec struct {
			Addons struct {
				CertManager struct {
					ACMEEmail string `json:"acmeEmail"`
				} `json:"certManager"`
				Longhorn struct {
					Enabled bool `json:"enabled"`
				} `json:"longhorn"`
			} `json:"addons"`
		} `json:"spec"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(manifest), &parsed), "the manifest must stay valid YAML:\n%s", manifest)
	require.Equal(t, "admin@acme.dev", parsed.Spec.Addons.CertManager.ACMEEmail)
	require.True(t, parsed.Spec.Addons.Longhorn.Enabled, "the add-ons that were already there are untouched")
}

func TestTheClusterSaysNothingAboutCertManagerWithoutAnEmail(t *testing.T) {
	manifest := geassClusterManifest("default", "v1", "https://127.0.0.1:6443", Network{}, "")
	require.NotContains(t, manifest, "certManager", "cert-manager keeps its default (installed), and the operator reports that no email was given")
	require.NoError(t, yaml.Unmarshal([]byte(manifest), &map[string]any{}))
}

func TestTheAcmeEmailAndTheNetworkChoiceCanBeGivenTogether(t *testing.T) {
	manifest := geassClusterManifest("default", "v1", "https://203.0.113.1:6443", Network{MultiSite: true, ExternalIP: "203.0.113.1"}, "admin@acme.dev")
	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(manifest), &parsed), manifest)
	spec := parsed["spec"].(map[string]any)
	require.Contains(t, spec, "network")
	require.Contains(t, spec["addons"], "certManager")
}
