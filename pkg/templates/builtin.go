package templates

import "github.com/degoke/geass/pkg/platform"

const databaseKey = "database"

// Builtin returns the templates that ship with Geass.
func Builtin() []Template {
	list := []Template{
		{
			ID:          "hello-function",
			Name:        "Hello function",
			Description: "A Bun function that answers every request. The smallest thing you can deploy.",
			Resources: []Resource{
				{Key: "hello", Kind: KindFunction, Name: "hello", Runtime: "bun", Code: platform.FunctionStarterCode("bun")},
			},
		},
		{
			ID:          "echo-demo",
			Name:        "HTTP echo",
			Description: "Echoes each request back with its headers and environment variables. Good for checking routing and variables.",
			Resources: []Resource{
				{
					Key: "echo", Kind: KindService, Name: "echo", Image: "mendhak/http-https-echo:41", Port: 8080,
					Variables: []Variable{
						{Name: "ECHO_INCLUDE_ENV_VARS", Value: "1"},
						{Name: "APP_NAME", Value: "geass-demo"},
						{Name: "APP_ENV", Value: "staging"},
						{Name: "FEATURE_FLAG", Value: "true"},
					},
				},
			},
		},
		{
			ID:          "postgres-adminer",
			Name:        "Postgres with Adminer",
			Description: "A PostgreSQL database and the Adminer web UI, pre-pointed at it.",
			Resources: []Resource{
				{Key: databaseKey, Kind: KindDatabase, Name: "postgres", Engine: "Postgres"},
				{
					Key: "adminer", Kind: KindService, Name: "adminer", Image: "adminer:4", Port: 8080,
					Variables: []Variable{
						{Name: "ADMINER_DEFAULT_SERVER", From: &Reference{Resource: databaseKey, Key: "host"}},
					},
				},
			},
		},
		{
			ID:          "function-postgres",
			Name:        "Function with Postgres",
			Description: "A Bun function that queries a PostgreSQL database through DATABASE_URL.",
			Resources: []Resource{
				{Key: databaseKey, Kind: KindDatabase, Name: "postgres", Engine: "Postgres"},
				{
					Key: "api", Kind: KindFunction, Name: "api", Runtime: "bun", Code: functionPostgresCode,
					Variables: []Variable{{Name: "DATABASE_URL", From: &Reference{Resource: databaseKey, Key: "uri"}}},
				},
			},
		},
		{
			ID:          "hasura-postgres",
			Name:        "Hasura GraphQL",
			Description: "Hasura GraphQL Engine on a PostgreSQL database, with the console enabled and a generated admin secret.",
			Resources: []Resource{
				{Key: databaseKey, Kind: KindDatabase, Name: "postgres", Engine: "Postgres"},
				{
					Key: "hasura", Kind: KindService, Name: "hasura", Image: "hasura/graphql-engine:v2.40.0", Port: 8080,
					Variables: []Variable{
						{Name: "HASURA_GRAPHQL_DATABASE_URL", From: &Reference{Resource: databaseKey, Key: "uri"}},
						{Name: "HASURA_GRAPHQL_ENABLE_CONSOLE", Value: "true"},
						{Name: "HASURA_GRAPHQL_ADMIN_SECRET", Generate: GenerateSecretKind},
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

// Find returns a built-in template by id.
func Find(id string) (Template, bool) {
	for _, template := range Builtin() {
		if template.ID == id {
			return template, true
		}
	}
	return Template{}, false
}

const functionPostgresCode = `import { sql } from "bun";

const port = Number(process.env.PORT) || 8080;

Bun.serve({
  port,
  async fetch() {
    try {
      const [row] = await sql` + "`select now() as now`" + `;
      return Response.json({ database: "connected", now: row.now });
    } catch (error) {
      return Response.json({ database: "unavailable", error: String(error) }, { status: 503 });
    }
  },
});

console.log("Listening on " + port);
`
