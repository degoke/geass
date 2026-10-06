package dashboard

import (
	"context"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/cloudflare"
	"github.com/degoke/geass/pkg/platform"
)

func (s *Server) handlePlatformCloudflareSettingsSave(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/settings/connectors") || !parseFormOrRedirect(w, r, "/settings/connectors") {
		return
	}
	apiToken := cloudflare.NormalizeAPIToken(r.FormValue("apiToken"))
	accountID := strings.TrimSpace(r.FormValue("accountId"))
	zoneID := strings.TrimSpace(r.FormValue("zoneId"))
	zoneName := strings.TrimSpace(r.FormValue("zoneName"))
	keepExisting := r.FormValue("keepExisting") == "on" || r.FormValue("keepExisting") == "true"

	if accountID == "" || zoneID == "" {
		redirectProbe(w, r, "/settings/connectors", dashboardLiteralError, "account ID and zone ID are required")
		return
	}
	if apiToken == "" && !keepExisting {
		redirectProbe(w, r, "/settings/connectors", dashboardLiteralError, "API token is required")
		return
	}

	secret := &corev1.Secret{}
	err := s.Client.Get(r.Context(), client.ObjectKey{Name: platform.PlatformCloudflareSecretName, Namespace: systemNamespace}, secret)
	if apierrors.IsNotFound(err) {
		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: platform.PlatformCloudflareSecretName, Namespace: systemNamespace},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{},
		}
	} else if err != nil {
		redirectProbe(w, r, "/settings/connectors", dashboardLiteralError, "could not load Cloudflare credentials")
		return
	}
	if apiToken != "" {
		secret.Data[platform.SecretKeyCloudflareAPIToken] = []byte(apiToken)
	}
	secret.Data[platform.SecretKeyCloudflareAccountID] = []byte(accountID)
	if zoneName == "" {
		zoneName = strings.TrimSpace(string(secret.Data[platform.SecretKeyCloudflareZoneName]))
	}
	if len(secret.Data[platform.SecretKeyCloudflareAPIToken]) == 0 {
		redirectProbe(w, r, "/settings/connectors", dashboardLiteralError, "API token is required")
		return
	}
	if err := s.persistSecret(r.Context(), secret); err != nil {
		redirectProbe(w, r, "/settings/connectors", dashboardLiteralError, "could not save Cloudflare credentials")
		return
	}

	cf := &cloudflare.Client{AccountID: accountID, APIToken: string(secret.Data[platform.SecretKeyCloudflareAPIToken]), HTTP: s.HTTPClient}
	if err := cf.Validate(r.Context()); err != nil {
		redirectProbe(w, r, "/settings/connectors", dashboardLiteralError, cloudflare.TokenValidationMessage(err))
		return
	}
	if zoneName == "" && zoneID != "" {
		if zone, err := cf.GetZone(r.Context(), zoneID); err == nil && strings.TrimSpace(zone.Name) != "" {
			zoneName = strings.TrimSpace(zone.Name)
		}
	}
	if zoneName == "" {
		redirectProbe(w, r, "/settings/connectors", dashboardLiteralError, "DNS zone name is required — pick a zone from the list")
		return
	}
	zoneRecords := parseCloudflareZonesForm(r.FormValue("zonesJson"))
	if len(zoneRecords) == 0 {
		zoneRecords = platform.CloudflareZonesFromSecret(secret)
	}
	if len(zoneRecords) == 0 {
		if listed, err := cf.ListZones(r.Context()); err == nil {
			for _, zone := range listed {
				zoneRecords = append(zoneRecords, platform.CloudflareZoneRecord{ID: zone.ID, Name: zone.Name})
			}
		}
	}
	zoneRecords = platform.EnsureCloudflareZoneInList(zoneRecords, zoneID, zoneName)
	platform.SetCloudflareZonesOnSecret(secret, zoneRecords)
	secret.Data[platform.SecretKeyCloudflareZoneName] = []byte(zoneName)
	if err := s.persistSecret(r.Context(), secret); err != nil {
		redirectProbe(w, r, "/settings/connectors", dashboardLiteralError, "could not save Cloudflare credentials")
		return
	}

	if err := s.attachCloudflareToPlatformConfig(r.Context(), zoneID); err != nil {
		redirectProbe(w, r, "/settings/connectors", dashboardLiteralError, "could not update platform config")
		return
	}
	redirect(w, r, "/settings/connectors")
}

func (s *Server) handlePlatformCloudflareRefresh(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/settings/connectors") {
		return
	}
	respond := func(errMsg string, zoneCount int) {
		if isJSONRequest(r) {
			if errMsg != "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: errMsg})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "zones": zoneCount})
			return
		}
		if errMsg != "" {
			redirectProbe(w, r, "/settings/connectors", dashboardLiteralError, errMsg)
			return
		}
		redirectProbe(w, r, "/settings/connectors", "success", "")
	}

	config, err := s.platformConfig(r.Context())
	if err != nil || !platform.CloudflareConfigured(config) {
		respond("Cloudflare is not connected", 0)
		return
	}
	secret := &corev1.Secret{}
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: platform.PlatformCloudflareSecretName, Namespace: systemNamespace}, secret); err != nil {
		respond("could not load Cloudflare credentials", 0)
		return
	}
	zoneID := strings.TrimSpace(config.Spec.CloudflareZoneID)
	zoneName := strings.TrimSpace(string(secret.Data[platform.SecretKeyCloudflareZoneName]))
	records, warning, err := s.syncCloudflareZonesFromAPI(r.Context(), secret, zoneID, zoneName)
	if err != nil {
		respond(err.Error(), 0)
		return
	}
	platform.SetCloudflareZonesOnSecret(secret, records)
	if err := s.persistSecret(r.Context(), secret); err != nil {
		respond("could not save refreshed zone list", 0)
		return
	}
	if isJSONRequest(r) {
		payload := map[string]any{"ok": true, "zones": len(records)}
		if warning != "" {
			payload["warning"] = warning
		}
		writeJSON(w, http.StatusOK, payload)
		return
	}
	if warning != "" {
		redirectProbe(w, r, "/settings/connectors", "warning", warning)
		return
	}
	redirectProbe(w, r, "/settings/connectors", "success", "")
}

func (s *Server) handlePlatformCloudflareClear(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/settings/connectors") || !parseFormOrRedirect(w, r, "/settings/connectors") {
		return
	}
	_ = s.Client.Delete(r.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: platform.PlatformCloudflareSecretName, Namespace: systemNamespace}})
	config, err := s.platformConfig(r.Context())
	if err == nil && config.Name != "" {
		config.Spec.CloudflareConnectionRef = nil
		config.Spec.CloudflareZoneID = ""
		_ = s.Client.Update(r.Context(), &config)
	}
	redirect(w, r, "/settings/connectors")
}

func (s *Server) persistSecret(ctx context.Context, secret *corev1.Secret) error {
	existing := &corev1.Secret{}
	err := s.Client.Get(ctx, client.ObjectKeyFromObject(secret), existing)
	if apierrors.IsNotFound(err) {
		return s.Client.Create(ctx, secret)
	}
	if err != nil {
		return err
	}
	existing.Data = secret.Data
	return s.Client.Update(ctx, existing)
}

func (s *Server) attachCloudflareToPlatformConfig(ctx context.Context, zoneID string) error {
	config, err := s.platformConfig(ctx)
	if err != nil {
		return err
	}
	creating := config.Name == ""
	if creating {
		config = geassv1alpha1.GeassPlatformConfig{
			ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: systemNamespace},
		}
	}
	config.Spec.CloudflareConnectionRef = &corev1.LocalObjectReference{Name: platform.PlatformCloudflareSecretName}
	config.Spec.CloudflareZoneID = zoneID
	if creating {
		return s.Client.Create(ctx, &config)
	}
	return s.Client.Update(ctx, &config)
}

func (s *Server) cloudflareSettingsInfo(ctx context.Context) (map[string]any, error) {
	config, err := s.platformConfig(ctx)
	if err != nil {
		return nil, err
	}
	connected := platform.CloudflareConfigured(config)
	ready := platform.IsConditionTrue(config.Status.Conditions, platform.ConditionCloudflareReady)
	info := map[string]any{
		"connected": connected,
		"ready":     ready,
		"zoneId":    config.Spec.CloudflareZoneID,
		"tunnelId":  config.Status.CloudflareTunnelID,
	}
	zoneID := strings.TrimSpace(config.Spec.CloudflareZoneID)
	var zoneName string
	if connected {
		summary := s.cloudflareConnectionSummary(ctx, config)
		if summary.AccountID != "" {
			info["accountId"] = summary.AccountID
		}
		if summary.ZoneID != "" && info["zoneId"] == "" {
			info["zoneId"] = summary.ZoneID
		}
		zoneName = summary.ZoneName
		if zoneName != "" {
			info["zoneName"] = zoneName
		}
		secret := &corev1.Secret{}
		if err := s.Client.Get(ctx, client.ObjectKey{Name: platform.PlatformCloudflareSecretName, Namespace: systemNamespace}, secret); err == nil {
			if cached := cloudflareZonesToAPI(platform.CloudflareZonesFromSecret(secret)); len(cached) > 0 {
				info["zones"] = cached
			} else if stored := storedCloudflareZones(zoneID, zoneName); len(stored) > 0 {
				info["zones"] = stored
			}
		} else if stored := storedCloudflareZones(zoneID, zoneName); len(stored) > 0 {
			info["zones"] = stored
		}
	} else if domain := platform.RootDomainFromConfig(config); domain != "" {
		info["zoneName"] = domain
	}
	rootDomain := platform.RootDomainFromConfig(config)
	if rootDomain == "" {
		if name, ok := info["zoneName"].(string); ok {
			rootDomain = strings.TrimSpace(name)
		}
	}
	if rootDomain != "" {
		info["suggestedRootDomain"] = rootDomain
		info["dashboardHost"] = platform.DashboardHostFromParts(platform.DashboardSubdomainFromConfig(config), rootDomain)
	}
	return info, nil
}

// storedCloudflareZones returns the zone saved at connect time (no Cloudflare API).
func storedCloudflareZones(zoneID, zoneName string) []cloudflare.Zone {
	zoneID = strings.TrimSpace(zoneID)
	zoneName = strings.TrimSpace(zoneName)
	if zoneID == "" || zoneName == "" {
		return nil
	}
	return []cloudflare.Zone{{ID: zoneID, Name: zoneName}}
}

func (s *Server) updateCloudflareZoneName(ctx context.Context, zoneID, rootDomain string) error {
	secret := &corev1.Secret{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: platform.PlatformCloudflareSecretName, Namespace: systemNamespace}, secret); err != nil {
		return err
	}
	if rootDomain != "" {
		secret.Data[platform.SecretKeyCloudflareZoneName] = []byte(rootDomain)
	}
	return s.Client.Update(ctx, secret)
}

func (s *Server) handleAPICloudflareDiscover(w http.ResponseWriter, r *http.Request) {
	if !s.sessionCanMutate(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: "forbidden"})
		return
	}
	if !sameOriginMutation(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: "request origin could not be verified"})
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "invalid request"})
		return
	}
	apiToken := cloudflare.NormalizeAPIToken(r.FormValue("apiToken"))
	if apiToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "API token is required"})
		return
	}
	cf := &cloudflare.Client{APIToken: apiToken, HTTP: s.HTTPClient}
	if err := cf.Validate(r.Context()); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: cloudflare.TokenValidationMessage(err)})
		return
	}
	accounts, err := cf.ListAccounts(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "could not list Cloudflare accounts"})
		return
	}
	zones, err := cf.ListZones(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "could not list Cloudflare zones"})
		return
	}
	if len(accounts) == 0 {
		seen := map[string]bool{}
		for _, zone := range zones {
			accountID := cloudflare.AccountIDFromZone(zone)
			if accountID == "" || seen[accountID] {
				continue
			}
			seen[accountID] = true
			name := ""
			if zone.Account != nil {
				name = zone.Account.Name
			}
			accounts = append(accounts, cloudflare.Account{ID: accountID, Name: name})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accounts": accounts,
		"zones":    zones,
	})
}

func (s *Server) handleAPICloudflareSettings(w http.ResponseWriter, r *http.Request) {
	config, err := s.platformConfig(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not load Cloudflare settings"})
		return
	}
	if !s.sessionCanMutate(r) {
		writeJSON(w, http.StatusOK, map[string]any{
			"connected": platform.CloudflareConfigured(config),
			"ready":     platform.IsConditionTrue(config.Status.Conditions, platform.ConditionCloudflareReady),
		})
		return
	}
	info, err := s.cloudflareSettingsInfo(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not load Cloudflare settings"})
		return
	}
	writeJSON(w, http.StatusOK, info)
}
