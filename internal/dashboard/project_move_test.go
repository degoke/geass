package dashboard

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/projectbundle"
)

// The values that must never reach a bundle, in a form that is plainly fake.
const (
	moveSecretValue   = "fixture-secret-value-for-the-move"
	moveConfigValue   = "fixture-visible-config-value"
	movePasswordValue = "fixture-password-value-for-the-move"
)

const (
	moveExport = "/api/v1/projects/pm1/export"
	moveImport = "/api/v1/projects/import"
)

// moveWorld puts a project that holds one of everything into organization A's side of a matrix cluster: a service that uses a
// database and keeps a secret, a function with a schedule, a database with backups on, a bucket, and a custom domain.
func moveWorld(t *testing.T, f *matrixFixture) {
	t.Helper()
	ctx := context.Background()
	meta := func(name string, annotations map[string]string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace, Annotations: annotations}
	}
	replicas := int32(2)
	size := resource.MustParse("5Gi")
	for _, object := range []client.Object{
		&geassv1alpha1.GeassProject{
			ObjectMeta: meta("pm1", nil),
			Spec: geassv1alpha1.GeassProjectSpec{
				DisplayName: "morning-beach", Org: orgA, ClusterRef: corev1.LocalObjectReference{Name: "default"}, Environments: []string{"production", "staging"},
				SharedVariables: []geassv1alpha1.GeassSharedVariable{
					{Name: "REGION_NAME", Environment: "production", Value: "eu"},
					{Name: "SIGNING_SECRET", Environment: "production", SecretRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "pm1-shared-secrets"}, Key: "production__SIGNING_SECRET"}},
				},
				Network:             geassv1alpha1.NewProjectNetwork(),
				GitHubConnectionRef: &corev1.LocalObjectReference{Name: "pm1-github"},
			},
		},
		&geassv1alpha1.GeassDatabase{
			ObjectMeta: meta("pm1-db", map[string]string{platform.DisplayNameAnnotation: "orders"}),
			Spec: geassv1alpha1.GeassDatabaseSpec{
				Project: "pm1", Environment: "production", Engine: geassv1alpha1.DatabaseEnginePostgres, Version: "16", StorageSize: &size,
				Backup: &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true, RetentionDays: 7}, Deploy: geassv1alpha1.GeassDeploySpec{Enabled: true},
			},
			Status: geassv1alpha1.GeassDatabaseStatus{ConnectionSecret: "pm1-db-connection"},
		},
		&geassv1alpha1.GeassObjectStore{
			ObjectMeta: meta("pm1-bucket", nil),
			Spec:       geassv1alpha1.GeassObjectStoreSpec{Project: "pm1", Environment: "production", Engine: geassv1alpha1.ObjectStoreEngineSeaweedFS, Buckets: []string{"uploads"}},
		},
		&geassv1alpha1.GeassApp{
			ObjectMeta: meta("pm1-web", map[string]string{platform.DisplayNameAnnotation: "storefront"}),
			Spec: geassv1alpha1.GeassAppSpec{
				Project: "pm1", Environment: "production", Source: imageAppSource("ghcr.io/acme/web:1.4"), Port: 3000, Replicas: &replicas,
				Ingress:    geassv1alpha1.GeassAppIngressSpec{Host: "shop.fixture.example", TLSEnabled: true},
				ConfigData: map[string]string{"LOG_LEVEL": moveConfigValue, "DB_PASSWORD": movePasswordValue},
				Env: []corev1.EnvVar{{Name: "DATABASE_URL", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "pm1-db-connection"}, Key: platform.ConnectionKeyURI}}}},
				SecretRef: &corev1.LocalObjectReference{Name: "pm1-web-secrets"},
				Deploy:    geassv1alpha1.GeassAppDeploySpec{Enabled: true},
			},
			Status: geassv1alpha1.GeassAppStatus{URL: "https://shop.fixture.example"},
		},
		&geassv1alpha1.GeassApp{
			ObjectMeta: meta("pm1-job", nil),
			Spec: geassv1alpha1.GeassAppSpec{
				Project: "pm1", Environment: "staging", Port: 8080,
				Source: geassv1alpha1.GeassAppSource{Function: &geassv1alpha1.GeassAppFunctionSource{
					Runtime: "node", Code: "require('http').createServer((q, s) => s.end('ok')).listen(process.env.PORT)\n",
					Triggers: []geassv1alpha1.GeassFunctionTrigger{{Name: "nightly", Schedule: "0 2 * * *"}},
				}},
			},
		},
		&corev1.Secret{ObjectMeta: meta("pm1-web-secrets", nil), Data: map[string][]byte{"SESSION_KEY": []byte(moveSecretValue)}},
		&corev1.Secret{ObjectMeta: meta("pm1-shared-secrets", nil), Data: map[string][]byte{"production__SIGNING_SECRET": []byte(moveSecretValue)}},
	} {
		require.NoError(t, f.base.Create(ctx, object))
	}
}

// doBody is do for a request whose body is bytes.
func (f *matrixFixture) doBody(session *dashboardSessionInfo, path, contentType string, body []byte) *httptest.ResponseRecorder {
	req := withOrigin(httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req = req.WithContext(context.WithValue(req.Context(), sessionContextKey{}, session))
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func adminOf(org string) *dashboardSessionInfo { return sessionIn(org, "", dashboardRoleAdmin) }

// exported is the bundle organization A's administrator gets of the project.
func (f *matrixFixture) exported(t *testing.T) []byte {
	t.Helper()
	rec := f.do(adminOf(orgA), http.MethodPost, moveExport, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return rec.Body.Bytes()
}

type moveAnswer struct {
	DryRun  bool `json:"dryRun"`
	OK      bool `json:"ok"`
	Project struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	Plan    *projectbundle.Plan `json:"plan"`
	Created []struct {
		Kind, Name, DisplayName, Environment, From string
	} `json:"created"`
	ToReenter []projectbundle.Item    `json:"toReenter"`
	Warnings  []projectbundle.Finding `json:"warnings"`
	Error     string                  `json:"error"`
	Code      string                  `json:"code"`
}

func decodeMove(t *testing.T, rec *httptest.ResponseRecorder) moveAnswer {
	t.Helper()
	var answer moveAnswer
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &answer), rec.Body.String())
	return answer
}

func (f *matrixFixture) importAs(session *dashboardSessionInfo, archive []byte, query string) *httptest.ResponseRecorder {
	return f.doBody(session, moveImport+query, "application/gzip", archive)
}

func moveListApps(t *testing.T, f *matrixFixture) []geassv1alpha1.GeassApp {
	t.Helper()
	var apps geassv1alpha1.GeassAppList
	require.NoError(t, f.base.List(context.Background(), &apps))
	return apps.Items
}

func projectsNamed(t *testing.T, f *matrixFixture, display string) []geassv1alpha1.GeassProject {
	t.Helper()
	var projects geassv1alpha1.GeassProjectList
	require.NoError(t, f.base.List(context.Background(), &projects))
	var out []geassv1alpha1.GeassProject
	for _, project := range projects.Items {
		if project.Spec.DisplayName == display {
			out = append(out, project)
		}
	}
	return out
}

func TestExportGivesTheDefinitionAndNoSecretValue(t *testing.T) {
	f := newMatrixFixture(t)
	moveWorld(t, f)
	before := f.snapshot(t)

	rec := f.do(adminOf(orgA), http.MethodPost, moveExport, nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "application/gzip", rec.Header().Get("Content-Type"))
	require.Equal(t, `attachment; filename="morning-beach-bundle.tar.gz"`, rec.Header().Get("Content-Disposition"))
	require.Equal(t, before, f.snapshot(t), "an export changes nothing")

	bundle, err := projectbundle.Decode(bytes.NewReader(rec.Body.Bytes()), projectbundle.DefaultLimits)
	require.NoError(t, err)
	require.Equal(t, "pm1", bundle.Manifest.Project.ID)
	require.Len(t, bundle.Services, 2)
	require.Len(t, bundle.Databases, 1)
	require.Len(t, bundle.Buckets, 1)

	files := unpackAll(t, rec.Body.Bytes())
	var everything, definitions strings.Builder
	for name, file := range files {
		everything.Write(file)
		if name != projectbundle.ManifestName {
			definitions.Write(file)
		}
	}
	for _, value := range []string{moveSecretValue, movePasswordValue} {
		require.NotContains(t, everything.String(), value)
	}
	require.Contains(t, definitions.String(), moveConfigValue, "plain configuration is the definition")
	for _, word := range []string{orgA, "pm1-web-secrets", "pm1-github", "geass-system"} {
		require.NotContains(t, definitions.String(), word, "the definitions name nothing of the cluster they came from")
	}

	reenter := map[string]bool{}
	for _, item := range bundle.Manifest.ToReenter {
		reenter[item.Kind+"/"+item.Item] = true
	}
	require.True(t, reenter["service/SESSION_KEY"], "the name of a secret key is listed so that it can be entered again")
	require.True(t, reenter["service/DB_PASSWORD"], "so is a value that looks like a secret")
	require.True(t, reenter["project/SIGNING_SECRET"])
}

// unpackAll reads an archive's files, as a person with tar would.
func unpackAll(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files
		}
		require.NoError(t, err)
		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		files[header.Name] = data
	}
}

func TestExportSummaryIsTheManifestAlone(t *testing.T) {
	f := newMatrixFixture(t)
	moveWorld(t, f)

	rec := f.do(adminOf(orgA), http.MethodPost, moveExport+"?summary=true", nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	var manifest projectbundle.Manifest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &manifest))
	require.Equal(t, projectbundle.Format, manifest.Format)
	require.Equal(t, projectbundle.Version, manifest.Version)
	require.NotEmpty(t, manifest.ToReenter)
	require.NotContains(t, rec.Body.String(), moveSecretValue)
}

func TestOnlyAnAdministratorOfTheProjectCanExportIt(t *testing.T) {
	f := newMatrixFixture(t)
	moveWorld(t, f)

	for name, tc := range map[string]struct {
		session *dashboardSessionInfo
		want    int
	}{
		"the organization's administrator":     {adminOf(orgA), http.StatusOK},
		"the platform's administrator":         {platformSession, http.StatusOK},
		"another organization's administrator": {adminOf(orgB), http.StatusNotFound},
		"a viewer of the organization":         {sessionIn(orgA, "", dashboardRoleViewer), http.StatusForbidden},
		// The project is in none of the organization's workspaces, so only the organization's own administrators see it.
		"an administrator of one workspace": {sessionIn(orgA, wsA2, dashboardRoleAdmin), http.StatusNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			rec := f.do(tc.session, http.MethodPost, moveExport, nil)

			require.Equal(t, tc.want, rec.Code, rec.Body.String())
			if tc.want == http.StatusNotFound {
				message, _ := decodeError(t, rec)
				require.Equal(t, "project was not found", message, "it is told what it is told of a project that does not exist")
			}
		})
	}
}

func TestExportAndImportAreNotForAProjectsOwnToken(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.workspaceToken(t, "admin")
	viewer := f.workspaceToken(t, "viewer")
	project := f.projectToken(t, "production")
	exportPath := "/api/v1/projects/" + testProjectName + "/export"

	require.Equal(t, http.StatusOK, f.do(jsonRequest(admin, http.MethodPost, exportPath, nil)).Code, "a workspace token with the admin role")
	for name, token := range map[string]string{"a viewer's": viewer, "a project's": project} {
		for _, path := range []string{exportPath, moveImport} {
			rec := f.do(jsonRequest(token, http.MethodPost, path, nil))
			require.Equal(t, http.StatusForbidden, rec.Code, "%s token on %s: %s", name, path, rec.Body.String())
		}
	}
}

func TestExportOfAProjectThatIsNotThereIsNotFound(t *testing.T) {
	f := newMatrixFixture(t)

	rec := f.do(platformSession, http.MethodPost, "/api/v1/projects/nothing/export", nil)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestImportMakesADraftProjectAndNothingRunsOrIsSecret(t *testing.T) {
	f := newMatrixFixture(t)
	moveWorld(t, f)
	g := newMatrixFixture(t) // the cluster the project is moved to
	archive := f.exported(t)
	sourceBefore := f.snapshot(t)
	var secretsBefore corev1.SecretList
	require.NoError(t, g.base.List(context.Background(), &secretsBefore))

	rec := g.importAs(adminOf(orgB), archive, "")

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	answer := decodeMove(t, rec)
	require.Regexp(t, `^proj-[0-9a-f]{8}$`, answer.Project.ID)
	require.NotEqual(t, "pm1", answer.Project.ID)
	require.Equal(t, "morning-beach", answer.Project.Name)

	var project geassv1alpha1.GeassProject
	require.NoError(t, g.base.Get(context.Background(), client.ObjectKey{Name: answer.Project.ID, Namespace: platform.SystemNamespace}, &project))
	require.Equal(t, orgB, project.Spec.Org, "the new project belongs to whoever imported it, and the bundle named no one")
	require.Empty(t, project.Spec.Workspace)
	require.Equal(t, "default", project.Spec.ClusterRef.Name)
	require.Equal(t, []string{"production", "staging"}, project.Spec.Environments)
	require.Equal(t, []geassv1alpha1.GeassSharedVariable{{Name: "REGION_NAME", Environment: "production", Value: "eu"}}, project.Spec.SharedVariables, "a secret shared variable is not made")
	require.Nil(t, project.Spec.GitHubConnectionRef)
	require.Equal(t, geassv1alpha1.NewProjectNetwork(), project.Spec.Network)
	require.Equal(t, "pm1", project.Annotations["geass.dev/imported-from"])
	require.NotEmpty(t, project.Annotations["geass.dev/imported-at"])
	require.NotEmpty(t, project.Annotations["geass.dev/imported-by"])

	var apps geassv1alpha1.GeassAppList
	var databases geassv1alpha1.GeassDatabaseList
	var stores geassv1alpha1.GeassObjectStoreList
	require.NoError(t, g.base.List(context.Background(), &apps))
	require.NoError(t, g.base.List(context.Background(), &databases))
	require.NoError(t, g.base.List(context.Background(), &stores))

	var web *geassv1alpha1.GeassApp
	var db *geassv1alpha1.GeassDatabase
	var store *geassv1alpha1.GeassObjectStore
	for i := range databases.Items {
		if databases.Items[i].Spec.Project == answer.Project.ID {
			db = &databases.Items[i]
		}
	}
	for i := range stores.Items {
		if stores.Items[i].Spec.Project == answer.Project.ID {
			store = &stores.Items[i]
		}
	}
	made := 0
	for i := range apps.Items {
		app := &apps.Items[i]
		if app.Spec.Project != answer.Project.ID {
			continue
		}
		made++
		require.False(t, app.Spec.Deploy.Enabled, "a service made by an import is a draft: nothing runs until it is deployed")
		require.Equal(t, "true", app.Annotations[platform.DeployWorkflowAnnotation])
		require.Equal(t, "created", app.Annotations[platform.PendingChangesAnnotation])
		require.Nil(t, app.Spec.SecretRef)
		require.NotContains(t, []string{"pm1-web", "pm1-job"}, app.Name, "objects are made under new names, which cannot clash with anyone's")
		if platform.ResourceDisplayName(app.ObjectMeta) == "storefront" {
			web = app
		}
	}
	require.Equal(t, 2, made)
	require.NotNil(t, web)
	require.NotNil(t, db)
	require.NotNil(t, store)

	require.False(t, db.Spec.Deploy.Enabled)
	require.Equal(t, "orders", platform.ResourceDisplayName(db.ObjectMeta))
	require.True(t, db.Spec.Backup.Enabled)
	require.Equal(t, "5Gi", db.Spec.StorageSize.String())
	require.False(t, store.Spec.Deploy.Enabled)
	require.Equal(t, geassv1alpha1.ObjectStoreEngineSeaweedFS, store.Spec.Engine, "a bucket is on the target's own object storage server")
	require.Equal(t, []string{"uploads"}, store.Spec.Buckets)

	require.Equal(t, "shop.fixture.example", web.Spec.Ingress.Host, "a domain of the person's stays")
	require.Equal(t, map[string]string{"LOG_LEVEL": moveConfigValue}, web.Spec.ConfigData)
	require.Len(t, web.Spec.Env, 1)
	require.Equal(t, db.Name+"-connection", web.Spec.Env[0].ValueFrom.SecretKeyRef.Name, "the variable that was attached to the old database is attached to the new one")

	var secretsAfter corev1.SecretList
	require.NoError(t, g.base.List(context.Background(), &secretsAfter))
	require.Len(t, secretsAfter.Items, len(secretsBefore.Items), "an import makes no Secret")
	require.Equal(t, sourceBefore, f.snapshot(t), "the source cluster is as it was")

	require.Len(t, answer.Created, 5)
	ids := map[string]string{}
	for _, created := range answer.Created {
		ids[created.Kind+"/"+created.From] = created.Name
	}
	require.Equal(t, web.Name, ids["apps/pm1-web"])
	reenter := map[string]string{}
	for _, item := range answer.ToReenter {
		reenter[item.Kind+"/"+item.Item] = item.ID
	}
	require.Equal(t, web.Name, reenter["service/SESSION_KEY"], "what is to be entered again names the new service")
	require.Equal(t, answer.Project.ID, reenter["project/SIGNING_SECRET"])
}

func TestADryRunSaysWhatWouldBeMadeAndChangesNothing(t *testing.T) {
	f := newMatrixFixture(t)
	moveWorld(t, f)
	g := newMatrixFixture(t) // the cluster the project is moved to
	archive := f.exported(t)
	before := g.snapshot(t)

	rec := g.importAs(adminOf(orgB), archive, "?dryRun=true")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	answer := decodeMove(t, rec)
	require.True(t, answer.DryRun)
	require.True(t, answer.OK)
	require.NotNil(t, answer.Plan)
	require.Equal(t, "morning-beach", answer.Plan.Project.Name)
	require.Len(t, answer.Plan.Create, 5)
	require.Contains(t, codesOf(answer.Plan.Warnings), "backup-storage-missing", "the database has backups on and the new project has nowhere to put them")
	require.NotEmpty(t, answer.Plan.ToReenter)
	require.NotEmpty(t, answer.Plan.Next)
	require.Equal(t, before, g.snapshot(t), "a dry run makes nothing")
}

func codesOf(findings []projectbundle.Finding) []string {
	out := make([]string, 0, len(findings))
	for _, finding := range findings {
		out = append(out, finding.Code)
	}
	return out
}

func TestAnImportAsJSONCarriesTheSameThings(t *testing.T) {
	f := newMatrixFixture(t)
	moveWorld(t, f)
	body, err := json.Marshal(map[string]any{"bundle": base64.StdEncoding.EncodeToString(f.exported(t)), "dryRun": true, "name": "Evening Shore"})
	require.NoError(t, err)

	rec := f.doBody(adminOf(orgB), moveImport, "application/json", body)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "evening-shore", decodeMove(t, rec).Plan.Project.Name)

	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"no bundle":    {`{}`, http.StatusBadRequest},
		"not base64":   {`{"bundle": "!!!"}`, http.StatusBadRequest},
		"not JSON":     {`{"bundle": `, http.StatusBadRequest},
		"not a bundle": {`{"bundle": "` + base64.StdEncoding.EncodeToString([]byte("hello")) + `"}`, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			rec := f.doBody(adminOf(orgB), moveImport, "application/json", []byte(tc.body))
			require.Equal(t, tc.want, rec.Code, rec.Body.String())
		})
	}
}

func TestAnImportRefusesANameThatTheCallerCanSeeIsTaken(t *testing.T) {
	f := newMatrixFixture(t)
	moveWorld(t, f)
	g := newMatrixFixture(t) // the cluster the project is moved to
	archive := f.exported(t)

	// Organization A has a project called morning-beach here: its own import of the same bundle is a clash.
	require.NoError(t, g.base.Create(context.Background(), &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "pg1", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{DisplayName: "morning-beach", Org: orgA, Environments: []string{"production"}},
	}))
	rec := g.importAs(adminOf(orgA), archive, "")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	answer := decodeMove(t, rec)
	require.Equal(t, "conflict", answer.Code)
	require.Contains(t, answer.Error, "morning-beach")
	require.Contains(t, codesOf(answer.Plan.Blockers), "project-name-taken")
	require.Len(t, projectsNamed(t, g, "morning-beach"), 1, "nothing was made")

	// Organization B cannot see that name, so it is no clash for it, and nothing says it exists.
	rec = g.importAs(adminOf(orgB), archive, "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Len(t, projectsNamed(t, g, "morning-beach"), 2)

	// And B's second import is B's own clash.
	rec = g.importAs(adminOf(orgB), archive, "")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Len(t, projectsNamed(t, g, "morning-beach"), 2)

	// Another name is no clash. (What stops it now is the hostname that B's first import took.)
	rec = g.importAs(adminOf(orgB), archive, "?name=evening-shore&dryRun=true")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotContains(t, codesOf(decodeMove(t, rec).Plan.Blockers), "project-name-taken")
}

func TestOnlyThePlatformCanKeepTheProjectsOldId(t *testing.T) {
	f := newMatrixFixture(t)
	moveWorld(t, f)
	g := newMatrixFixture(t) // the cluster the project is moved to
	archive := f.exported(t)
	rec := g.importAs(adminOf(orgB), archive, "?keepProjectId=true")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Contains(t, codesOf(decodeMove(t, rec).Plan.Blockers), "keep-project-id-refused")
	require.Empty(t, projectsNamed(t, g, "morning-beach"))

	rec = g.importAs(platformSession, archive, "?keepProjectId=true")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, "pm1", decodeMove(t, rec).Project.ID)

	rec = g.importAs(platformSession, archive, "?keepProjectId=true&name=other")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Contains(t, codesOf(decodeMove(t, rec).Plan.Blockers), "project-id-taken")
}

func TestAnImportThatTheClusterCannotTakeMakesNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		prepare func(t *testing.T, f *matrixFixture)
		want    string
	}{
		"no object storage": {func(t *testing.T, f *matrixFixture) {
			require.NoError(t, f.base.Delete(context.Background(), &geassv1alpha1.GeassObjectStore{ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterObjectStoreName, Namespace: platform.SystemNamespace}}))
		}, "object-storage-missing"},
		"a hostname that another organization's service has": {func(t *testing.T, f *matrixFixture) {
			other := tenantApp("held", "pb1")
			other.Spec.Ingress.Host = "shop.fixture.example"
			require.NoError(t, f.base.Create(context.Background(), other))
		}, "host-unavailable"},
		"an organization at its project limit": {func(t *testing.T, f *matrixFixture) {
			var org geassv1alpha1.GeassOrganization
			require.NoError(t, f.base.Get(context.Background(), client.ObjectKey{Name: orgB, Namespace: platform.SystemNamespace}, &org))
			limit := int32(1)
			org.Spec.MaxProjects = &limit
			require.NoError(t, f.base.Update(context.Background(), &org))
		}, "project-limit"},
		"an organization that may use other domains": {func(t *testing.T, f *matrixFixture) {
			var org geassv1alpha1.GeassOrganization
			require.NoError(t, f.base.Get(context.Background(), client.ObjectKey{Name: orgB, Namespace: platform.SystemNamespace}, &org))
			org.Spec.AllowedDomains = []string{"b-owned.example"}
			require.NoError(t, f.base.Update(context.Background(), &org))
		}, "host-unavailable"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newMatrixFixture(t)
			moveWorld(t, f)
			g := newMatrixFixture(t) // the cluster the project is moved to
			archive := f.exported(t)
			tc.prepare(t, g)
			before := g.snapshot(t)

			dry := g.importAs(adminOf(orgB), archive, "?dryRun=true")
			require.Equal(t, http.StatusOK, dry.Code, dry.Body.String())
			require.False(t, decodeMove(t, dry).OK)
			require.Contains(t, codesOf(decodeMove(t, dry).Plan.Blockers), tc.want)

			rec := g.importAs(adminOf(orgB), archive, "")

			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			answer := decodeMove(t, rec)
			require.Contains(t, codesOf(answer.Plan.Blockers), tc.want)
			require.NotEmpty(t, answer.Error)
			require.NotContains(t, rec.Body.String(), "pb1", "the answer names no project of another organization")
			require.Equal(t, before, g.snapshot(t), "a refused import makes nothing")
		})
	}
}

// failingCreates fails the creation of the nth service, the way a cluster that has something wrong does.
type failingCreates struct {
	client.Client
	mu     sync.Mutex
	failAt int
	apps   int
}

func (c *failingCreates) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if _, ok := obj.(*geassv1alpha1.GeassApp); ok {
		c.mu.Lock()
		c.apps++
		fail := c.apps == c.failAt
		c.mu.Unlock()
		if fail {
			return errors.New("the cluster refused it")
		}
	}
	return c.Client.Create(ctx, obj, opts...)
}

func TestAnImportThatFailsHalfWayRemovesWhatItMade(t *testing.T) {
	f := newMatrixFixture(t)
	moveWorld(t, f)
	g := newMatrixFixture(t) // the cluster the project is moved to
	archive := f.exported(t)
	before := g.snapshot(t)
	g.srv.Client = &failingCreates{Client: g.srv.Client, failAt: 2}

	rec := g.importAs(adminOf(orgB), archive, "")

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "refused it", "what went wrong inside is not for the caller")
	require.Equal(t, before, g.snapshot(t), "the project, its database, its bucket and its first service are gone again")
	require.Empty(t, projectsNamed(t, g, "morning-beach"))
}

func TestABundleThatIsNotOneOrIsDangerousIsRefused(t *testing.T) {
	f := newMatrixFixture(t)
	moveWorld(t, f)
	g := newMatrixFixture(t) // the cluster the project is moved to
	good := f.exported(t)
	before := g.snapshot(t)

	bundle, err := projectbundle.Decode(bytes.NewReader(good), projectbundle.DefaultLimits)
	require.NoError(t, err)
	bundle.Services[0].Spec.SecretRef = &corev1.LocalObjectReference{Name: "geass-dashboard-auth"}
	doctored, err := bundle.Encode()
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		body []byte
		want int
	}{
		"text":               {[]byte("hello"), http.StatusBadRequest},
		"a truncated bundle": {good[:len(good)/2], http.StatusBadRequest},
		"a service that names a Secret of the cluster": {doctored, http.StatusBadRequest},
		"nothing": {nil, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			rec := g.importAs(adminOf(orgB), tc.body, "")
			require.Equal(t, tc.want, rec.Code, rec.Body.String())
			message, code := decodeError(t, rec)
			require.NotEmpty(t, message)
			require.Equal(t, "invalid_request", code)
		})
	}
	require.Equal(t, before, g.snapshot(t))

	message, _ := decodeError(t, g.importAs(adminOf(orgB), doctored, ""))
	require.Contains(t, message, "secrets")
	require.NotContains(t, message, "geass-dashboard-auth", "what the bundle named is not read back out of the cluster")
}

func TestABundleOverTheLimitIsRefusedAsTooLarge(t *testing.T) {
	overrideVar(t, &projectbundle.DefaultLimits, projectbundle.Limits{MaxArchiveBytes: 2 << 10, MaxExpandedBytes: 64 << 20, MaxFileBytes: 1 << 20, MaxFiles: 500})
	f := newMatrixFixture(t)

	rec := f.importAs(adminOf(orgB), bytes.Repeat([]byte{1}, 3<<10), "")

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
	_, code := decodeError(t, rec)
	require.Equal(t, "too_large", code)
}

func TestOnlyAnAdministratorCanImport(t *testing.T) {
	f := newMatrixFixture(t)
	moveWorld(t, f)
	g := newMatrixFixture(t) // the cluster the project is moved to
	archive := f.exported(t)
	before := g.snapshot(t)

	rec := g.importAs(sessionIn(orgB, "", dashboardRoleViewer), archive, "")

	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Equal(t, before, g.snapshot(t))
}

func TestAWorkspaceAdministratorImportsIntoTheirOwnWorkspace(t *testing.T) {
	f := newMatrixFixture(t)
	moveWorld(t, f)
	g := newMatrixFixture(t) // the cluster the project is moved to
	archive := f.exported(t)

	rec := g.importAs(sessionIn(orgA, wsA2, dashboardRoleAdmin), archive, "?name=in-the-workspace")

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var project geassv1alpha1.GeassProject
	require.NoError(t, g.base.Get(context.Background(), client.ObjectKey{Name: decodeMove(t, rec).Project.ID, Namespace: platform.SystemNamespace}, &project))
	require.Equal(t, orgA, project.Spec.Org)
	require.Equal(t, wsA2, project.Spec.Workspace)
}

func TestExportAndImportAreLoggedWithWhoAndHowMuchAndNeverWhat(t *testing.T) {
	f := newMatrixFixture(t)
	moveWorld(t, f)
	g := newMatrixFixture(t) // the cluster the project is moved to
	var mu sync.Mutex
	var lines []string
	logger := funcr.New(func(prefix, args string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, prefix+args)
	}, funcr.Options{})
	exportRequest := func() *httptest.ResponseRecorder {
		req := withOrigin(httptest.NewRequest(http.MethodPost, moveExport, nil))
		req.Header.Set("Accept", "application/json")
		ctx := context.WithValue(req.Context(), sessionContextKey{}, adminOf(orgA))
		req = req.WithContext(ctrllog.IntoContext(ctx, logger))
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)
		return rec
	}
	rec := exportRequest()
	require.Equal(t, http.StatusOK, rec.Code)

	importRequest := withOrigin(httptest.NewRequest(http.MethodPost, moveImport, bytes.NewReader(rec.Body.Bytes())))
	importRequest.Header.Set("Content-Type", "application/gzip")
	ctx := context.WithValue(importRequest.Context(), sessionContextKey{}, adminOf(orgB))
	importRequest = importRequest.WithContext(ctrllog.IntoContext(ctx, logger))
	imported := httptest.NewRecorder()
	g.handler.ServeHTTP(imported, importRequest)
	require.Equal(t, http.StatusCreated, imported.Code, imported.Body.String())

	mu.Lock()
	defer mu.Unlock()
	all := strings.Join(lines, "\n")
	require.Contains(t, all, `"Exported project"`)
	require.Contains(t, all, `"Imported project"`)
	require.Contains(t, all, `"by"="person@`+orgA)
	require.Contains(t, all, `"by"="person@`+orgB)
	require.Contains(t, all, `"project"="pm1"`)
	require.Contains(t, all, `"toReenter"=`)
	for _, value := range []string{moveSecretValue, movePasswordValue, moveConfigValue} {
		require.NotContains(t, all, value)
	}
}

func TestTheOperationsAreInTheAPIDescription(t *testing.T) {
	var found []string
	for _, operation := range v1Operations {
		if operation.Path == "/projects/import" || operation.Path == "/projects/{name}/export" {
			found = append(found, operation.Method+" "+operation.Path)
			require.Equal(t, accessAdmin, operation.Access)
		}
	}
	require.ElementsMatch(t, []string{"POST /projects/import", "POST /projects/{name}/export"}, found)
	require.Equal(t, scopeTenant, routeScopeOf(http.MethodPost, "/api/projects/import"))
	require.Equal(t, scopeTenant, routeScopeOf(http.MethodPost, "/api/projects/p1/export"))
}

func TestAProjectCannotBeImportedIntoAClusterThatStillServesItsDomain(t *testing.T) {
	f := newMatrixFixture(t)
	moveWorld(t, f)
	archive := f.exported(t)
	before := f.snapshot(t)

	// The same cluster: the source's storefront still answers on shop.fixture.example, so the copy cannot have it.
	rec := f.importAs(adminOf(orgA), archive, "?name=a-copy")

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	answer := decodeMove(t, rec)
	require.Contains(t, codesOf(answer.Plan.Blockers), "host-unavailable")
	require.Contains(t, answer.Error, "another service")
	require.Equal(t, before, f.snapshot(t))
}
