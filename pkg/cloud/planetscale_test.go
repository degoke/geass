package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlanetScaleAuthorizationUsesBearer(t *testing.T) {
	require.Equal(t, "Bearer pscale_token", planetScaleAuthorization("pscale_token"))
	require.Equal(t, "Bearer pscale_token", planetScaleAuthorization("Bearer pscale_token"))
	require.Equal(t, "Bearer pscale_token", planetScaleAuthorization("bearer pscale_token"))
}

func TestPlanetScaleClientSendsBearerAuthorization(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"name":"app"}`))
	}))
	t.Cleanup(srv.Close)

	client := &PlanetScaleClient{HTTP: srv.Client(), Token: "pscale_token", Org: "acme", Base: srv.URL}
	_, err := client.EnsureDatabase(context.Background(), "app", PlanetScaleKindMySQL, "")
	require.NoError(t, err)
	require.Equal(t, "Bearer pscale_token", got)
}

func TestPlanetScaleClientFailsWhenResponseExceedsLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("a", s3ListResponseLimit+1)))
	}))
	t.Cleanup(srv.Close)

	client := &PlanetScaleClient{HTTP: srv.Client(), Token: "pscale_token", Org: "acme", Base: srv.URL}
	_, err := client.EnsureDatabase(context.Background(), "app", PlanetScaleKindMySQL, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeded")
}

// planetScaleRecorder serves canned responses by "METHOD path" and records every request body.
type planetScaleRecorder struct {
	responses map[string]string
	calls     []string
	bodies    map[string]map[string]any
}

func newPlanetScaleServer(t *testing.T, responses map[string]string) (*PlanetScaleClient, *planetScaleRecorder) {
	t.Helper()
	rec := &planetScaleRecorder{responses: responses, bodies: map[string]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		rec.calls = append(rec.calls, key)
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err == nil {
				rec.bodies[key] = body
			}
		}
		payload, ok := rec.responses[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	return &PlanetScaleClient{HTTP: srv.Client(), Token: "pscale_token", Org: "acme", Base: srv.URL}, rec
}

func TestPlanetScaleEnsureDatabaseCreatesTheRequestedKind(t *testing.T) {
	for _, kind := range []string{PlanetScaleKindMySQL, PlanetScaleKindPostgres} {
		t.Run(kind, func(t *testing.T) {
			client, rec := newPlanetScaleServer(t, map[string]string{"POST /organizations/acme/databases": `{"name":"app"}`})
			_, err := client.EnsureDatabase(context.Background(), "app", kind, "")
			require.NoError(t, err)
			require.Equal(t, []string{"GET /organizations/acme/databases/app", "POST /organizations/acme/databases"}, rec.calls)
			require.Equal(t, map[string]any{"name": "app", "kind": kind}, rec.bodies["POST /organizations/acme/databases"])
		})
	}
}

func TestPlanetScaleEnsureDatabaseReusesADatabaseOfTheSameKind(t *testing.T) {
	client, rec := newPlanetScaleServer(t, map[string]string{"GET /organizations/acme/databases/app": `{"name":"app","kind":"postgresql","ready":true}`})
	database, err := client.EnsureDatabase(context.Background(), "app", PlanetScaleKindPostgres, "PS_10")
	require.NoError(t, err)
	require.True(t, database.Ready)
	require.Equal(t, []string{"GET /organizations/acme/databases/app"}, rec.calls, "an existing database is not created again")
}

func TestPlanetScaleEnsureDatabaseRejectsADatabaseOfAnotherKind(t *testing.T) {
	client, rec := newPlanetScaleServer(t, map[string]string{"GET /organizations/acme/databases/app": `{"name":"app","kind":"mysql"}`})
	_, err := client.EnsureDatabase(context.Background(), "app", PlanetScaleKindPostgres, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "already exists as mysql, not postgresql")
	require.Equal(t, []string{"GET /organizations/acme/databases/app"}, rec.calls, "nothing is created over a database of another kind")
}

func TestPlanetScaleCreateCredentialsUsesAPasswordForMySQL(t *testing.T) {
	const path = "POST /organizations/acme/databases/app/branches/main/passwords"
	client, rec := newPlanetScaleServer(t, map[string]string{
		path: `{"id":"pw-1","username":"u1","plain_text":"p1","access_host_url":"aws.connect.psdb.cloud"}`,
	})
	creds, err := client.CreateCredentials(context.Background(), "app", PlanetScaleKindMySQL, "orders")
	require.NoError(t, err)
	require.Equal(t, PlanetScaleCredentials{ID: "pw-1", Host: "aws.connect.psdb.cloud", Username: "u1", Password: "p1"}, creds)
	require.Equal(t, []string{path}, rec.calls)
	require.Equal(t, map[string]any{"name": "orders"}, rec.bodies[path])
}

func TestPlanetScaleCreateCredentialsUsesARoleForPostgres(t *testing.T) {
	const path = "POST /organizations/acme/databases/app/branches/main/roles"
	client, rec := newPlanetScaleServer(t, map[string]string{
		path: `{"id":"role-1","username":"u2","password":"p2","access_host_url":"aws.connect.psdb.cloud","database_name":"postgres"}`,
	})
	creds, err := client.CreateCredentials(context.Background(), "app", PlanetScaleKindPostgres, "orders")
	require.NoError(t, err)
	require.Equal(t, PlanetScaleCredentials{ID: "role-1", Host: "aws.connect.psdb.cloud", Username: "u2", Password: "p2", Database: "postgres"}, creds)
	require.Equal(t, []string{path}, rec.calls, "Postgres databases have no branch passwords")
	require.Equal(t, map[string]any{"name": "orders", "inherited_roles": []any{"postgres"}}, rec.bodies[path])
}

func TestPlanetScaleCreateCredentialsRejectsAnUnknownKind(t *testing.T) {
	client, rec := newPlanetScaleServer(t, nil)
	_, err := client.CreateCredentials(context.Background(), "app", "redis", "orders")
	require.Error(t, err)
	require.Empty(t, rec.calls)
}

func TestPlanetScaleEnsureDatabaseReportsWhetherItIsReady(t *testing.T) {
	client, _ := newPlanetScaleServer(t, map[string]string{"POST /organizations/acme/databases": `{"name":"app","kind":"mysql","ready":false}`})
	created, err := client.EnsureDatabase(context.Background(), "app", PlanetScaleKindMySQL, "")
	require.NoError(t, err)
	require.False(t, created.Ready, "a database that was just created is still being provisioned")

	client, _ = newPlanetScaleServer(t, map[string]string{"GET /organizations/acme/databases/app": `{"name":"app","kind":"mysql","ready":true}`})
	existing, err := client.EnsureDatabase(context.Background(), "app", PlanetScaleKindMySQL, "")
	require.NoError(t, err)
	require.True(t, existing.Ready)
}

func TestPlanetScaleEnsureDatabaseSendsTheClusterSizeOnlyWhenChosen(t *testing.T) {
	const create = "POST /organizations/acme/databases"
	client, rec := newPlanetScaleServer(t, map[string]string{create: `{"name":"app"}`})
	_, err := client.EnsureDatabase(context.Background(), "app", PlanetScaleKindMySQL, "PS_10")
	require.NoError(t, err)
	require.Equal(t, map[string]any{"name": "app", "kind": "mysql", "cluster_size": "PS_10"}, rec.bodies[create])

	client, rec = newPlanetScaleServer(t, map[string]string{create: `{"name":"app"}`})
	_, err = client.EnsureDatabase(context.Background(), "app", PlanetScaleKindPostgres, "")
	require.NoError(t, err)
	require.NotContains(t, rec.bodies[create], "cluster_size", "an unchosen size is left to PlanetScale")
}

func TestPlanetScaleListClusterSizesKeepsEnabledSizesSmallestFirst(t *testing.T) {
	const path = "GET /organizations/acme/cluster-size-skus"
	client, rec := newPlanetScaleServer(t, map[string]string{path: `[
		{"name":"PS_80","display_name":"PS-80","cpu":"4","ram":17179869184,"storage":null,"enabled":true,"sort_order":30,"production":true},
		{"name":"PS_5","display_name":"PS-5","cpu":"1/8","ram":536870912,"storage":10737418240,"enabled":false,"sort_order":5,"development":true},
		{"name":"PS_10","display_name":"PS-10","cpu":"1/4","ram":1073741824,"storage":10737418240,"enabled":true,"sort_order":10,"development":true}
	]`})
	sizes, err := client.ListClusterSizes(context.Background(), PlanetScaleKindPostgres, false)
	require.NoError(t, err)

	require.Len(t, sizes, 2, "a size the organization cannot use is not offered")
	require.Equal(t, "PS_10", sizes[0].Name)
	require.Equal(t, "PS_80", sizes[1].Name)
	require.Equal(t, "1/4", sizes[0].CPU)
	require.NotNil(t, sizes[0].Storage)
	require.Equal(t, int64(10737418240), *sizes[0].Storage)
	require.Nil(t, sizes[1].Storage, "a size with elastic storage has no fixed amount")
	require.True(t, sizes[0].Development)
	require.True(t, sizes[1].Production)
	require.Nil(t, sizes[0].Rate, "no price was asked for")
	require.Equal(t, []string{path}, rec.calls)
}

func TestPlanetScaleListClusterSizesReadsPricesAndTellsSizesApart(t *testing.T) {
	client, _ := newPlanetScaleServer(t, map[string]string{"GET /organizations/acme/cluster-size-skus": `[
		{"name":"PS_10_X86","display_name":"PS-10","cpu":"1/8","ram":1073741824,"enabled":true,"sort_order":10,
		 "rate":39,"replica_rate":13,"provider":"aws","architecture":"x86_64","metal":false},
		{"name":"PS_10_ARM","display_name":"PS-10","cpu":"1/8","ram":1073741824,"enabled":true,"sort_order":11,
		 "rate":30,"replica_rate":10,"provider":"aws","architecture":"arm64","metal":false},
		{"name":"PS_5","display_name":"PS-5","cpu":"1/16","ram":536870912,"enabled":true,"sort_order":1,
		 "rate":null,"replica_rate":null,"provider":null,"architecture":null,"metal":true}
	]`})
	sizes, err := client.ListClusterSizes(context.Background(), PlanetScaleKindPostgres, true)
	require.NoError(t, err)
	require.Len(t, sizes, 3)

	byName := map[string]PlanetScaleClusterSize{}
	for _, size := range sizes {
		byName[size.Name] = size
	}
	x86, arm, free := byName["PS_10_X86"], byName["PS_10_ARM"], byName["PS_5"]
	require.NotNil(t, x86.Rate)
	require.Equal(t, 39.0, *x86.Rate, "the cluster price, in whole dollars")
	require.Equal(t, 13.0, *x86.ReplicaRate, "one node costs a third of the cluster")
	require.Equal(t, "x86_64", *x86.Architecture)
	require.Equal(t, "aws", *x86.Provider)
	require.Equal(t, 30.0, *arm.Rate, "the same size is cheaper on arm64, so the two must be told apart")
	require.Equal(t, "arm64", *arm.Architecture)
	require.Nil(t, free.Rate, "a null price stays absent instead of reading as free")
	require.Nil(t, free.ReplicaRate)
	require.True(t, free.Metal)
}

func TestPlanetScaleListClusterSizesAsksForTheKindsSizes(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	client := &PlanetScaleClient{HTTP: srv.Client(), Token: "pscale_token", Org: "acme", Base: srv.URL}

	_, err := client.ListClusterSizes(context.Background(), PlanetScaleKindPostgres, false)
	require.NoError(t, err)
	require.Equal(t, "engine=postgresql", query, "MySQL and Postgres have different sizes")

	_, err = client.ListClusterSizes(context.Background(), PlanetScaleKindMySQL, true)
	require.NoError(t, err)
	require.Equal(t, "engine=mysql&rates=true", query, "prices are only sent when asked for")
}

// A database name is the user's, and it goes into the path of requests made with the organization's token.
func TestPlanetScaleEscapesNamesInThePathsItCalls(t *testing.T) {
	var escaped, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escaped, query = r.URL.EscapedPath(), r.URL.RawQuery
		_, _ = w.Write([]byte(`{"name":"found","kind":"mysql","ready":true}`))
	}))
	t.Cleanup(srv.Close)
	client := &PlanetScaleClient{HTTP: srv.Client(), Token: "pscale_token", Org: "acme", Base: srv.URL}

	_, err := client.EnsureDatabase(context.Background(), "x/branches/main/passwords?y#z", PlanetScaleKindMySQL, "")
	require.NoError(t, err)
	require.Equal(t, "/organizations/acme/databases/x%2Fbranches%2Fmain%2Fpasswords%3Fy%23z", escaped, "the name stays one path segment")
	require.Empty(t, query, "a question mark in the name does not start a query")

	_, err = client.CreateCredentials(context.Background(), "../other", PlanetScaleKindPostgres, "orders")
	require.NoError(t, err)
	require.Equal(t, "/organizations/acme/databases/..%2Fother/branches/main/roles", escaped)
}

func TestPlanetScaleRefusesNamesThatAreNotOnePathSegment(t *testing.T) {
	client, rec := newPlanetScaleServer(t, nil)
	for _, name := range []string{"", ".", ".."} {
		_, err := client.EnsureDatabase(context.Background(), name, PlanetScaleKindMySQL, "")
		require.Error(t, err, "database %q", name)
		require.Error(t, client.RevokeCredentials(context.Background(), "app", PlanetScaleKindMySQL, name), "login id %q", name)
	}
	client.Org = ".."
	_, err := client.ListClusterSizes(context.Background(), PlanetScaleKindMySQL, false)
	require.Error(t, err, "an organization of '..' would reach another endpoint")
	require.Empty(t, rec.calls, "nothing is sent for a name that cannot be one segment")
}

// access_host_url is a host for a branch password, but PlanetScale's role docs call it a connection string.
func TestPlanetScaleCredentialsKeepOnlyTheHost(t *testing.T) {
	cases := []struct{ name, reported, want string }{
		{"a bare host", "aws.connect.psdb.cloud", "aws.connect.psdb.cloud"},
		{"a host and port", "aws.connect.psdb.cloud:5432", "aws.connect.psdb.cloud"},
		{"a connection string", "postgresql://u2:p2@aws.connect.psdb.cloud:5432/postgres?sslmode=require", "aws.connect.psdb.cloud"},
		{"a URL", "https://aws.connect.psdb.cloud/", "aws.connect.psdb.cloud"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := newPlanetScaleServer(t, map[string]string{
				"POST /organizations/acme/databases/app/branches/main/roles": `{"id":"r","username":"u2","password":"p2","access_host_url":"` + tc.reported + `"}`,
			})
			creds, err := client.CreateCredentials(context.Background(), "app", PlanetScaleKindPostgres, "orders")
			require.NoError(t, err)
			require.Equal(t, tc.want, creds.Host)
		})
	}
}

func TestPlanetScaleRevokeCredentialsDeletesTheLoginOfTheKind(t *testing.T) {
	cases := []struct{ kind, call string }{
		{PlanetScaleKindMySQL, "DELETE /organizations/acme/databases/app/branches/main/passwords/pw-1"},
		{PlanetScaleKindPostgres, "DELETE /organizations/acme/databases/app/branches/main/roles/pw-1"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			client, rec := newPlanetScaleServer(t, map[string]string{tc.call: ``})
			require.NoError(t, client.RevokeCredentials(context.Background(), "app", tc.kind, "pw-1"))
			require.Equal(t, []string{tc.call}, rec.calls)
		})
	}
}

func TestPlanetScaleRevokeCredentialsTreatsAGoneLoginAsRevoked(t *testing.T) {
	client, _ := newPlanetScaleServer(t, nil) // every unknown path is a 404
	require.NoError(t, client.RevokeCredentials(context.Background(), "app", PlanetScaleKindMySQL, "pw-1"))
}

func TestPlanetScaleErrorsCarryTheStatusCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"forbidden"}`))
	}))
	t.Cleanup(srv.Close)
	client := &PlanetScaleClient{HTTP: srv.Client(), Token: "pscale_token", Org: "acme", Base: srv.URL}

	err := client.RevokeCredentials(context.Background(), "app", PlanetScaleKindMySQL, "pw-1")
	var apiErr *PlanetScaleAPIError
	require.True(t, errors.As(err, &apiErr), "the caller must be able to tell a refusal from an outage")
	require.Equal(t, http.StatusForbidden, apiErr.StatusCode)
	require.Contains(t, err.Error(), "forbidden")
}
