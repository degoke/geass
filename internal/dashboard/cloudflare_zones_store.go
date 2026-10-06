package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/degoke/geass/pkg/cloudflare"
	"github.com/degoke/geass/pkg/platform"
)

func parseCloudflareZonesForm(raw string) []platform.CloudflareZoneRecord {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var zones []platform.CloudflareZoneRecord
	if err := json.Unmarshal([]byte(raw), &zones); err != nil {
		return nil
	}
	return platform.NormalizeCloudflareZoneRecords(zones)
}

func cloudflareZonesToAPI(zones []platform.CloudflareZoneRecord) []cloudflare.Zone {
	out := make([]cloudflare.Zone, 0, len(zones))
	for _, zone := range zones {
		out = append(out, cloudflare.Zone{ID: zone.ID, Name: zone.Name})
	}
	return out
}

func cloudflareRequestError(err error) string {
	if err == nil {
		return "Cloudflare request failed"
	}
	msg := strings.TrimSpace(err.Error())
	if after, ok := strings.CutPrefix(msg, "cloudflare API:"); ok {
		return after
	}
	return cloudflare.TokenValidationMessage(err)
}

// syncCloudflareZonesFromAPI re-fetches zones using the stored token. It does not use
// /user/tokens/verify so refresh matches what zone-scoped tokens can actually do.
func (s *Server) syncCloudflareZonesFromAPI(ctx context.Context, secret *corev1.Secret, zoneID, zoneName string) ([]platform.CloudflareZoneRecord, string, error) {
	if secret == nil {
		return nil, "", fmt.Errorf("Cloudflare credentials are missing")
	}
	token := cloudflare.NormalizeAPIToken(string(secret.Data[platform.SecretKeyCloudflareAPIToken]))
	if token == "" {
		return nil, "", fmt.Errorf("API token is missing")
	}
	if zoneName == "" {
		zoneName = strings.TrimSpace(string(secret.Data[platform.SecretKeyCloudflareZoneName]))
	}
	cached := platform.CloudflareZonesFromSecret(secret)

	cf := &cloudflare.Client{
		AccountID: strings.TrimSpace(string(secret.Data[platform.SecretKeyCloudflareAccountID])),
		APIToken:  token,
		HTTP:      s.HTTPClient,
	}

	records := []platform.CloudflareZoneRecord{}
	var listErr error
	if listed, err := cf.ListZones(ctx); err == nil {
		for _, zone := range listed {
			records = append(records, platform.CloudflareZoneRecord{ID: zone.ID, Name: zone.Name})
		}
	} else {
		listErr = err
	}

	zoneID = strings.TrimSpace(zoneID)
	if len(records) == 0 && zoneID != "" {
		if zone, err := cf.GetZone(ctx, zoneID); err == nil && strings.TrimSpace(zone.Name) != "" {
			records = append(records, platform.CloudflareZoneRecord{ID: zone.ID, Name: zone.Name})
			zoneName = strings.TrimSpace(zone.Name)
		}
	}

	if len(records) > 0 {
		return platform.EnsureCloudflareZoneInList(records, zoneID, zoneName), "", nil
	}

	if len(cached) > 0 && zoneID != "" {
		if _, err := cf.GetZone(ctx, zoneID); err == nil {
			return platform.EnsureCloudflareZoneInList(cached, zoneID, zoneName),
				"Could not list all zones; kept your saved zone list",
				nil
		}
	}

	if listErr != nil {
		return nil, "", fmt.Errorf("%s", cloudflareRequestError(listErr))
	}
	if len(cached) > 0 {
		return platform.EnsureCloudflareZoneInList(cached, zoneID, zoneName),
			"Could not refresh zones from Cloudflare; kept your saved zone list",
			nil
	}
	return nil, "", fmt.Errorf("no zones returned — add Zone read permission to your API token")
}
