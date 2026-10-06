package platform

import (
	"encoding/json"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// CloudflareZoneRecord is a DNS zone cached when the operator connects Cloudflare.
type CloudflareZoneRecord struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func CloudflareZonesFromSecret(secret *corev1.Secret) []CloudflareZoneRecord {
	if secret == nil || secret.Data == nil {
		return nil
	}
	raw := secret.Data[SecretKeyCloudflareZones]
	if len(raw) == 0 {
		return nil
	}
	var zones []CloudflareZoneRecord
	if err := json.Unmarshal(raw, &zones); err != nil {
		return nil
	}
	return NormalizeCloudflareZoneRecords(zones)
}

func SetCloudflareZonesOnSecret(secret *corev1.Secret, zones []CloudflareZoneRecord) {
	if secret == nil {
		return
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	zones = NormalizeCloudflareZoneRecords(zones)
	if len(zones) == 0 {
		delete(secret.Data, SecretKeyCloudflareZones)
		return
	}
	payload, err := json.Marshal(zones)
	if err != nil {
		return
	}
	secret.Data[SecretKeyCloudflareZones] = payload
}

func NormalizeCloudflareZoneRecords(zones []CloudflareZoneRecord) []CloudflareZoneRecord {
	seen := map[string]bool{}
	out := make([]CloudflareZoneRecord, 0, len(zones))
	for _, zone := range zones {
		id := strings.TrimSpace(zone.ID)
		name := strings.TrimSpace(zone.Name)
		if id == "" || name == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, CloudflareZoneRecord{ID: id, Name: name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func EnsureCloudflareZoneInList(zones []CloudflareZoneRecord, id, name string) []CloudflareZoneRecord {
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	if id == "" || name == "" {
		return NormalizeCloudflareZoneRecords(zones)
	}
	for _, zone := range zones {
		if zone.ID == id {
			return NormalizeCloudflareZoneRecords(zones)
		}
	}
	return NormalizeCloudflareZoneRecords(append(zones, CloudflareZoneRecord{ID: id, Name: name}))
}
