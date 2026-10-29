package dashboard

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/internal/mailrelay"
	"github.com/degoke/geass/pkg/platform"
)

// GET /api/v1/mail/usage is what the platform's mail relay (spec.tenancy.mail.relay) counted for each organization: what
// its windows hold now, which its rate limits are checked against, and what it sent, was refused and could not send since
// the relay started. It is the platform's alone, like the usage hours: one organization's mail is not another's to know.
// routeScopeOf makes the route the platform's, and the handler asks again.
//
// The dashboard asks the relay, on its admin port, with the admin token from the relay's Secret. The counts live in the
// relay's memory, in its one replica, so the answer says since when.

// mailRelayUsageTimeout bounds asking the relay.
const mailRelayUsageTimeout = 5 * time.Second

// mailRelayURL is where the dashboard reaches the relay's admin port: the relay's Service, or GEASS_MAIL_RELAY_URL for a
// dashboard that runs outside the cluster.
func mailRelayURL() string {
	if v := os.Getenv("GEASS_MAIL_RELAY_URL"); v != "" {
		return strings.TrimSuffix(v, "/")
	}
	return "http://" + platform.MailRelayHost() + ":" + strconv.Itoa(int(platform.MailRelayAdminPort))
}

// v1MailUsageAnswer is the answer.
type v1MailUsageAnswer struct {
	// Enabled says the relay runs. Without it the answer has no organizations.
	Enabled bool `json:"enabled"`
	// Counters says where the counts are kept: "memory", in the relay's one replica.
	Counters string `json:"counters"`
	// CountedSince is when the relay started: every count is since then, and a restart starts them over.
	CountedSince *time.Time `json:"countedSince,omitempty"`
	// Now is when the relay answered.
	Now           *time.Time           `json:"now,omitempty"`
	Org           string               `json:"org,omitempty"`
	Organizations []mailrelay.OrgUsage `json:"organizations"`
}

func (s *Server) v1MailUsage(w http.ResponseWriter, r *http.Request) {
	if s.refuseScopedToken(w, r) {
		return
	}
	if !s.v1Caller(r).canMutate() || !isPlatformContext(r.Context()) {
		writeV1Error(w, http.StatusForbidden, "only the platform's administrators can read what each organization sent through the mail relay")
		return
	}
	org := r.URL.Query().Get(usageQueryOrg)
	if org != "" && !dnsLabel.MatchString(org) {
		writeV1Error(w, http.StatusBadRequest, "org must be the name of an organization")
		return
	}
	ctx := asSystem(r.Context())
	relay, err := platform.PlatformMailRelay(ctx, s.Client)
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not read the platform configuration")
		return
	}
	answer := v1MailUsageAnswer{Counters: "memory", Org: org, Organizations: []mailrelay.OrgUsage{}}
	if relay == nil {
		writeJSON(w, http.StatusOK, answer)
		return
	}
	answer.Enabled = true

	secret := &corev1.Secret{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: platform.MailRelayName, Namespace: platform.SystemNamespace}, secret); err != nil ||
		len(secret.Data[platform.MailRelayAdminTokenKey]) == 0 {
		writeV1Error(w, http.StatusServiceUnavailable, "the mail relay has not been set up yet: see the MailRelayReady condition of the platform configuration")
		return
	}
	usage, err := fetchMailRelayUsage(r, string(secret.Data[platform.MailRelayAdminTokenKey]))
	if err != nil {
		writeV1Error(w, http.StatusServiceUnavailable, "the mail relay did not answer: "+err.Error())
		return
	}
	answer.CountedSince, answer.Now = &usage.StartedAt, &usage.Now
	for _, entry := range usage.Organizations {
		if org == "" || entry.Org == org {
			answer.Organizations = append(answer.Organizations, entry)
		}
	}
	writeJSON(w, http.StatusOK, answer)
}

// fetchMailRelayUsage asks the relay what it counted. The token is never in what an error says.
func fetchMailRelayUsage(r *http.Request, token string) (*mailrelay.Usage, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, mailRelayURL()+"/usage", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: mailRelayUsageTimeout}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not connect to it at %s", mailRelayURL())
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("it answered %d", resp.StatusCode)
	}
	var usage mailrelay.Usage
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&usage); err != nil {
		return nil, fmt.Errorf("its answer could not be read")
	}
	return &usage, nil
}
