package mailrelay

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const adminToken = "admin-token-0123456789abcdefghijklmnopqrstuvwxyz"

func get(t *testing.T, handler http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestMetricsAndUsageNeedTheAdminToken(t *testing.T) {
	h := newHarness(t, testOrg("org-acme", acmePassword, roomy()))
	require.NoError(t, sendOne(h.signedIn(t, "org-acme", acmePassword)))
	handler := h.relay.AdminHandler(func() string { return adminToken })

	for _, path := range []string{"/metrics", "/usage"} {
		require.Equal(t, http.StatusUnauthorized, get(t, handler, path, "").Code, path)
		require.Equal(t, http.StatusUnauthorized, get(t, handler, path, adminToken+"x").Code, path)
		require.Equal(t, http.StatusUnauthorized, get(t, handler, path, adminToken[:10]).Code, path)
		require.Equal(t, http.StatusOK, get(t, handler, path, adminToken).Code, path)
	}
	require.Equal(t, http.StatusUnauthorized, get(t, h.relay.AdminHandler(func() string { return "" }), "/usage", "").Code,
		"without a token nobody is let in")
	require.Equal(t, http.StatusOK, get(t, handler, "/healthz", "").Code)
	require.Equal(t, http.StatusOK, get(t, handler, "/readyz", "").Code)
	require.Equal(t, http.StatusServiceUnavailable, get(t, New(Options{}).AdminHandler(func() string { return adminToken }), "/readyz", "").Code,
		"a relay without its configuration and certificate is not ready")

	metrics := get(t, handler, "/metrics", adminToken).Body.String()
	require.Contains(t, metrics, `geass_mail_relay_messages_accepted_total{org="org-acme"} 1`)
	require.Contains(t, metrics, `geass_mail_relay_recipients_accepted_total{org="org-acme"} 1`)
	require.Contains(t, metrics, `geass_mail_relay_connections{org="org-acme"} 1`)
	require.Contains(t, metrics, `geass_mail_relay_upstream_duration_seconds_count{result="accepted"} 1`)

	var usage Usage
	require.NoError(t, json.Unmarshal(get(t, handler, "/usage", adminToken).Body.Bytes(), &usage))
	require.Equal(t, testStart, usage.StartedAt)
	require.Len(t, usage.Organizations, 1)
	require.Equal(t, "org-acme", usage.Organizations[0].Org)
	require.Equal(t, int64(1), usage.Organizations[0].Accepted)
	require.Equal(t, int64(1), usage.Organizations[0].LastDay)
	require.Equal(t, roomy(), usage.Organizations[0].Limits)
	require.NotNil(t, usage.Organizations[0].LastAcceptedAt)
}

func TestRejectionsAreCountedByOrganizationAndReason(t *testing.T) {
	limits := roomy()
	limits.MessagesPerMinute = 1
	h := newHarness(t, testOrg("org-acme", acmePassword, limits))
	client := h.signedIn(t, "org-acme", acmePassword)
	require.NoError(t, sendOne(client))
	require.Error(t, sendOne(client))
	require.Error(t, h.connect(t).Auth(plainLogin("org-acme", "wrong")))
	require.Error(t, h.connect(t).Auth(plainLogin("somebody", "wrong")))
	metrics := get(t, h.relay.AdminHandler(func() string { return adminToken }), "/metrics", adminToken).Body.String()
	require.Contains(t, metrics, `geass_mail_relay_rejections_total{org="org-acme",reason="rate_minute"} 1`)
	require.Contains(t, metrics, `geass_mail_relay_auth_failures_total{org="org-acme"} 1`)
	require.Contains(t, metrics, `geass_mail_relay_auth_failures_total{org="unknown"} 1`)
	require.NotContains(t, metrics, "somebody", "what someone typed never becomes a label")
}

// writeFiles writes what the operator's Secrets hold into a directory, as the kubelet would.
func writeFiles(t *testing.T, dir string, config *Config, token string) {
	t.Helper()
	data, err := json.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "admin-token"), []byte(token), 0o600))
}

func TestTheRelayReadsItsFilesAgainAndKeepsWhatWorksWhenOneIsWrong(t *testing.T) {
	dir := t.TempDir()
	_, _, pair := testCertificate(t)
	certEnd := strings.Index(string(pair), "-----BEGIN EC PRIVATE KEY-----")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tls.crt"), pair[:certEnd], 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tls.key"), pair[certEnd:], 0o600))
	upstreamDir := filepath.Join(dir, "upstream")
	require.NoError(t, os.Mkdir(upstreamDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "username"), []byte("relay\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "password"), []byte("upstream-password"), 0o600))
	config := &Config{Upstream: Upstream{Host: "smtp.example.net", Port: 587, TLS: TLSStartTLS}, Organizations: []Organization{testOrg("org-acme", acmePassword, roomy())}}
	writeFiles(t, dir, config, adminToken+"\n")

	logs := &syncBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	relay := New(Options{Logger: log})
	token := &tokenHolder{}
	f := &files{config: filepath.Join(dir, "config.json"), token: filepath.Join(dir, "admin-token"),
		cert: filepath.Join(dir, "tls.crt"), key: filepath.Join(dir, "tls.key"), upstream: upstreamDir}
	f.reload(relay, token, log)
	require.True(t, relay.Ready())
	require.Equal(t, adminToken, token.get())
	require.Equal(t, &UpstreamCredentials{Username: "relay", Password: "upstream-password"}, relay.credentials.Load())
	require.Len(t, relay.current.Load().config.Organizations, 1)

	config.Organizations = append(config.Organizations, testOrg("org-new", otherPassword, roomy()))
	writeFiles(t, dir, config, "rotated-token")
	f.reload(relay, token, log)
	require.Len(t, relay.current.Load().config.Organizations, 2)
	require.Equal(t, "rotated-token", token.get())

	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"upstream":{"host":"x","port":0,"tls":"Sometimes"}}`), 0o600))
	f.reload(relay, token, log)
	require.Len(t, relay.current.Load().config.Organizations, 2, "a configuration that is wrong leaves the one that worked")
	require.Contains(t, logs.String(), "could not read the relay's configuration")
	require.NotContains(t, logs.String(), "upstream-password")
	require.NotContains(t, logs.String(), "rotated-token")
	metrics := get(t, relay.AdminHandler(token.get), "/metrics", "rotated-token").Body.String()
	require.Contains(t, metrics, `geass_mail_relay_config_reloads_total{result="failed"} 1`)
	require.Contains(t, metrics, `geass_mail_relay_config_reloads_total{result="ok"} 2`)
}

func TestRunRefusesToStartWithoutItsFiles(t *testing.T) {
	var stderr strings.Builder
	require.Equal(t, 2, Run([]string{"--listen=127.0.0.1:0"}, io.Discard, &stderr))
	require.Contains(t, stderr.String(), "--config, --admin-token, --tls-cert and --tls-key are required")
	require.Equal(t, 2, Run([]string{"--no-such-flag"}, io.Discard, io.Discard))
}

func TestAConfigurationThatIsWrongIsRefused(t *testing.T) {
	for name, config := range map[string]string{
		"not JSON":                 `{`,
		"an unknown TLS mode":      `{"upstream":{"host":"h","port":587,"tls":"Maybe"},"organizations":[]}`,
		"no port":                  `{"upstream":{"host":"h","port":0,"tls":"None"},"organizations":[]}`,
		"a username taken twice":   `{"upstream":{"host":"h","port":587,"tls":"None"},"organizations":[` + orgJSON("a", "u") + `,` + orgJSON("b", "u") + `]}`,
		"a digest that is not one": `{"upstream":{"host":"h","port":587,"tls":"None"},"organizations":[{"name":"a","username":"a","credentials":[{"sha256":"zz"}],"limits":{"recipientsPerMessage":1,"maxMessageBytes":1,"connections":1}}]}`,
		"limits out of range":      `{"upstream":{"host":"h","port":587,"tls":"None"},"organizations":[{"name":"a","username":"a","credentials":[],"limits":{"messagesPerMinute":-1,"recipientsPerMessage":1,"maxMessageBytes":1,"connections":1}}]}`,
	} {
		_, err := ParseConfig([]byte(config))
		require.Error(t, err, name)
	}
	_, err := ParseConfig([]byte(`{"upstream":{"host":"h","port":587,"tls":"None"},"organizations":[` + orgJSON("a", "a") + `]}`))
	require.NoError(t, err)
}

func orgJSON(name, username string) string {
	return `{"name":"` + name + `","username":"` + username + `","credentials":[{"sha256":"` + PasswordDigest("p") + `"}],"limits":{"recipientsPerMessage":1,"maxMessageBytes":1,"connections":1}}`
}
