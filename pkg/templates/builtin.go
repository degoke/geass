package templates

const (
	databaseKey = "database"
	redisKey    = "redis"
)

// Builtin returns the catalog: self-hosted software that ships with Geass and can be deployed in one
// step. Each entry pins the image it was tested with and sizes it from what that image was measured to
// use: what it needs to run is reserved. The ceiling is either what it needs only for a short while, such
// as its first start, or, where an entry says so, headroom for more data that was not measured. Starter
// examples live in the documentation, not here.
func Builtin() []Template {
	list := []Template{
		{
			ID:          "gatus",
			Name:        "Gatus",
			Description: "A status page and uptime monitor. It checks your endpoints on a schedule and shows how they have been doing.",
			Notes:       "Add checks in the config.yaml variable. Results are kept in memory, so they reset when the service restarts.",
			Resources: []Resource{
				{
					Key: "gatus", Kind: KindService, Name: "gatus", Image: "twinproduction/gatus:v5.37.0", Port: 8080,
					Files:     map[string]string{"config.yaml": gatusConfig},
					Variables: []Variable{{Name: "GATUS_CONFIG_PATH", Value: "/config/config.yaml"}},
				},
			},
		},
		{
			ID:          "infisical",
			Name:        "Infisical",
			Description: "Open-source secrets management for teams: store, share and sync secrets and certificates across environments.",
			Notes: "Add a domain, then set SITE_URL to its address. Keep a copy of ENCRYPTION_KEY: without it the stored secrets cannot be read. " +
				"The first start takes a few minutes.",
			Resources: []Resource{
				{Key: databaseKey, Kind: KindDatabase, Name: "postgres", Engine: "Postgres"},
				{Key: redisKey, Kind: KindDatabase, Name: "redis", Engine: "Redis"},
				{
					Key: "infisical", Kind: KindService, Name: "infisical", Image: "infisical/infisical:v0.166.8", Port: 8080,
					Size: &Size{CPU: "500m", Memory: "1Gi", MemoryMax: "2Gi"}, // 720 MiB at rest, 960 MiB on first start
					Variables: []Variable{
						{Name: "DB_CONNECTION_URI", From: &Reference{Resource: databaseKey, Key: "uri"}},
						{Name: "REDIS_URL", From: &Reference{Resource: redisKey, Key: "uri"}},
						{Name: "ENCRYPTION_KEY", Generate: GenerateSecretKind},
						{Name: "AUTH_SECRET", Generate: GenerateSecretKind},
						{Name: "SITE_URL", Value: "http://localhost:8080"},
						{Name: "NODE_ENV", Value: "production"},
					},
				},
			},
		},
		{
			ID:          "refhir",
			Name:        "ReFHIR",
			Description: "A FHIR R4 server for health data, with a search index, an audit trail and optional SMART App Launch, on a Postgres database.",
			Notes: "The first start takes under a minute. Keep a copy of REFHIR_AUDIT_KEY: it keys the audit trail. Until you turn on SMART " +
				"sign-in, anyone who can reach the service can read and write health data, so add a domain only after you have secured it. " +
				"To install another implementation guide, add REFHIR_PACKAGE_DOWNLOAD=true (it needs internet access).",
			Resources: []Resource{
				{Key: databaseKey, Kind: KindDatabase, Name: "postgres", Engine: "Postgres"},
				{
					Key: "refhir", Kind: KindService, Name: "refhir", Image: "ghcr.io/hai-hq/refhir:0.0.2", Port: 8080,
					// Measured on about 900 resources, a small dataset: 40 to 76 MiB at rest, 105 to 109 MiB after the busiest
					// test and a 107 to 122 MiB peak. Raise the reserve if real data pushes it past 256 MiB; the 1 GiB
					// ceiling is headroom that was not measured.
					Size: &Size{CPU: "250m", Memory: "256Mi", CPUMax: "1", MemoryMax: "1Gi"},
					Variables: []Variable{
						{Name: "REFHIR_POSTGRES_URL", From: &Reference{Resource: databaseKey, Key: "uri"}},
						{Name: "REFHIR_AUDIT_KEY", Generate: GenerateSecretKind},
					},
				},
			},
		},
		{
			ID:          "browserbay",
			Name:        "browserbay",
			Description: "Self-hosted browsers for AI agents and automation. Your services ask it for a Chromium and drive it over the DevTools protocol.",
			Notes: "Serve it over HTTPS on a domain before anything outside the cluster uses it: the API key and the browser tokens travel to it. " +
				"It runs up to 5 browsers at once, about 1.5 GB for five busy pages. A browser can open any address the service can reach, " +
				"so give browser tokens only to services you trust.",
			Resources: []Resource{
				{
					Key: "browserbay", Kind: KindService, Name: "browserbay", Image: "ghcr.io/degoke/browserbay:0.2.2", Port: 8788,
					Size: &Size{CPU: "500m", Memory: "1Gi", CPUMax: "2", MemoryMax: "4Gi"}, // 0.2 to 0.5 GB idle, 1.5 GB with five busy pages
					Variables: []Variable{
						{Name: "BROWSERBAY_API_KEY", Generate: GenerateSecretKind},
					},
				},
			},
		},
	}
	for i := range list {
		list[i].Builtin = true
	}
	return list
}

// Find returns a catalog entry by id.
func Find(id string) (Template, bool) {
	for _, template := range Builtin() {
		if template.ID == id {
			return template, true
		}
	}
	return Template{}, false
}

// gatusConfig is the starting config: one check of Gatus itself, so the page shows something real
// before anything of the user's is added.
const gatusConfig = `endpoints:
  - name: Gatus
    group: Geass
    url: "http://localhost:8080/health"
    interval: 1m
    conditions:
      - "[STATUS] == 200"
      - "[BODY].status == UP"
`
