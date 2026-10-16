package templates

const (
	databaseKey = "database"
	redisKey    = "redis"
)

// Builtin returns the catalog: self-hosted software that ships with Geass and can be deployed in one
// step. Each entry pins the image it was tested with and sizes it from what that image was measured to
// use: what it needs to run is reserved, and what it needs only for a short while, such as its first
// start, is a ceiling. Starter examples live in the documentation, not here.
func Builtin() []Template {
	list := []Template{
		{
			ID:          "gatus",
			Name:        "Gatus",
			Description: "A status page and uptime monitor. It checks your endpoints on a schedule and shows how they have been doing.",
			Notes: "Gatus reads its endpoints from the config.yaml variable on the service's Variables tab. It starts with one check, " +
				"of itself. Add your own, then deploy the change. Results are kept in memory, so they reset when the service restarts.",
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
			Notes: "Add a domain to the infisical service, then set SITE_URL to its address. Geass generated ENCRYPTION_KEY and " +
				"AUTH_SECRET for you. Keep a copy of ENCRYPTION_KEY: without it the stored secrets cannot be read. " +
				"The first start runs database migrations and takes a few minutes.",
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
			Notes: "The first start downloads the FHIR packages from packages.fhir.org, so the service needs outbound internet access. " +
				"It takes about two minutes and uses up to 3.1 GB of memory while it does; the process then keeps about 2 GB until it " +
				"restarts, and needs about 1.3 GB after that. GET /readyz answers once it is ready. Keep a copy of REFHIR_AUDIT_KEY: " +
				"it keys the audit trail, which GET /_audit/verify checks. Until you turn on SMART sign-in, anyone who can reach the " +
				"service can read and write health data, so add a domain only after you have secured it.",
			Resources: []Resource{
				{Key: databaseKey, Kind: KindDatabase, Name: "postgres", Engine: "Postgres"},
				{
					Key: "refhir", Kind: KindService, Name: "refhir", Image: "ghcr.io/hai-hq/refhir:0.0.1", Port: 8080,
					Size: &Size{CPU: "500m", Memory: "2Gi", CPUMax: "2", MemoryMax: "4Gi"}, // 1.3 GB after a restart, 3.1 GB on first start
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
			Notes: "Add a domain to the browserbay service and serve it over HTTPS before anything outside the cluster uses it: the API key " +
				"and the tokens for each browser travel to it. Geass generated BROWSERBAY_API_KEY; your services present it to take a browser. " +
				"It runs up to 5 browsers at once, and five busy pages used about 1.5 GB. A browser can open any address the service can " +
				"reach, so give browser tokens only to services you trust.",
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
