package platform

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func projectDatabase(project string, engine geassv1alpha1.GeassDatabaseEngine) *geassv1alpha1.GeassDatabase {
	return &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Project: project, Environment: "production", Engine: engine},
	}
}

func sharedDatabase(org string, engine geassv1alpha1.GeassDatabaseEngine) *geassv1alpha1.GeassDatabase {
	return &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-db", Namespace: SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Org: org, Engine: engine},
	}
}

func TestADatabaseServerIsSandboxedWhenItIsAnOrganizations(t *testing.T) {
	ctx := context.Background()
	c := sandboxClient(t, sandboxConfig(false), runtimeClass(sandboxClass), orgProject("shop", sandboxOrg), orgProject("internal", ""))
	for name, tc := range map[string]struct {
		db   *geassv1alpha1.GeassDatabase
		want Sandbox
	}{
		"a database of an organization's project": {projectDatabase("shop", geassv1alpha1.DatabaseEngineMySQL), Sandbox{RuntimeClass: sandboxClass, Applies: true, Org: sandboxOrg}},
		"a shared server the organization owns":   {sharedDatabase(sandboxOrg, geassv1alpha1.DatabaseEngineMySQL), Sandbox{RuntimeClass: sandboxClass, Applies: true, Org: sandboxOrg}},
		"a database of a project that is missing": {projectDatabase("gone", geassv1alpha1.DatabaseEngineMySQL), Sandbox{RuntimeClass: sandboxClass, Applies: true}},
		"a database of the platform's project":    {projectDatabase("internal", geassv1alpha1.DatabaseEngineMySQL), Sandbox{}},
		"the platform's shared server":            {sharedDatabase("", geassv1alpha1.DatabaseEngineMySQL), Sandbox{}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ResolveDatabaseSandbox(ctx, c, tc.db, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	// The same decision as for services, so the same fail-closed error and the same reserved-node rule.
	missing := sandboxClient(t, sandboxConfig(false), orgProject("shop", sandboxOrg))
	_, err := ResolveDatabaseSandbox(ctx, missing, sharedDatabase(sandboxOrg, geassv1alpha1.DatabaseEngineRedis), nil)
	var wait *SandboxRuntimeMissingError
	require.True(t, errors.As(err, &wait))
	reserved := sandboxClient(t, sandboxConfig(true), organization(sandboxOrg, reservedTag), taggedNode("n1", reservedTag, true))
	got, err := ResolveDatabaseSandbox(ctx, reserved, sharedDatabase(sandboxOrg, geassv1alpha1.DatabaseEngineRedis), onReservedNodes())
	require.NoError(t, err, "the class is not needed on reserved nodes")
	require.Equal(t, reservedTag, got.ReservedTag)
	require.Empty(t, got.RuntimeClass)
}

func TestAnEngineThatCannotRunInTheSandboxIsRefusedOnlyWhereItWouldHaveTo(t *testing.T) {
	ctx := context.Background()
	on := sandboxClient(t, sandboxConfig(true), orgProject("shop", sandboxOrg), organization(sandboxOrg, reservedTag), taggedNode("n1", reservedTag, true))
	for name, tc := range map[string]struct {
		db       *geassv1alpha1.GeassDatabase
		selector map[string]string
		refused  bool
	}{
		"Postgres for an organization's project":          {projectDatabase("shop", geassv1alpha1.DatabaseEnginePostgres), nil, true},
		"MariaDB for an organization":                     {sharedDatabase(sandboxOrg, geassv1alpha1.DatabaseEngineMariaDB), nil, true},
		"an engine named by nothing, which is Postgres":   {projectDatabase("shop", ""), nil, true},
		"Postgres on nodes reserved for the organization": {projectDatabase("shop", geassv1alpha1.DatabaseEnginePostgres), onReservedNodes(), false},
		"MySQL for an organization":                       {projectDatabase("shop", geassv1alpha1.DatabaseEngineMySQL), nil, false},
		"Postgres for the platform":                       {sharedDatabase("", geassv1alpha1.DatabaseEnginePostgres), nil, false},
		"an external Postgres, which runs nowhere in the cluster": {func() *geassv1alpha1.GeassDatabase {
			db := projectDatabase("shop", geassv1alpha1.DatabaseEnginePostgres)
			db.Spec.Placement = geassv1alpha1.DatabasePlacementExternal
			return db
		}(), nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			gap, err := DatabaseSandboxGap(ctx, on, tc.db, tc.selector)
			require.NoError(t, err, "a missing RuntimeClass is a wait, not a refusal")
			require.Equal(t, tc.refused, gap != "", gap)
		})
	}

	off := sandboxClient(t, orgProject("shop", sandboxOrg))
	gap, err := DatabaseSandboxGap(ctx, off, projectDatabase("shop", geassv1alpha1.DatabaseEnginePostgres), nil)
	require.NoError(t, err)
	require.Empty(t, gap, "without the setting nothing is refused")
}

func TestTheMessagesOfAnUnsandboxableEngineSayWhichAndWhatToDo(t *testing.T) {
	gap := EngineSandboxGap(geassv1alpha1.DatabaseEnginePostgres)
	require.Contains(t, gap, "CloudNativePG")
	require.Contains(t, gap, CNPGChartVersion)
	refused := EngineCannotBeSandboxedMessage(gap, false)
	require.Contains(t, refused, "PostgreSQL runs on the CloudNativePG operator")
	require.Contains(t, refused, "it is not made")
	require.Contains(t, refused, "Choose MySQL, Redis or SQLite instead")
	held := EngineCannotBeSandboxedMessage(EngineSandboxGap(geassv1alpha1.DatabaseEngineMariaDB), true)
	require.Contains(t, held, "MariaDB runs on mariadb-operator")
	require.Contains(t, held, "keeps running as it is, outside the sandbox")
	require.Contains(t, EngineCannotBeGivenMessage(gap), "not given to an organization")
	for _, message := range []string{refused, held, EngineCannotBeGivenMessage(gap)} {
		require.True(t, strings.Contains(message, MessageCannotBeSandboxed))
		require.False(t, ReconcileFailureShouldCount(message))
	}
	require.Contains(t, SandboxRestartPendingMessage("", sandboxClass), "Runs without the sandbox until it is restarted into RuntimeClass gvisor")
	require.Contains(t, SandboxRestartPendingMessage(sandboxClass, "kata"), "Runs under RuntimeClass gvisor until it is restarted into RuntimeClass kata")
	require.Contains(t, SandboxRestartPendingMessage(sandboxClass, ""), "until it is restarted to run without it")
	require.False(t, DatabaseRestartsImmediately(nil))
	require.False(t, DatabaseRestartsImmediately(&geassv1alpha1.GeassTenantSandboxSpec{RuntimeClassName: sandboxClass}), "approval is the default")
	require.True(t, DatabaseRestartsImmediately(&geassv1alpha1.GeassTenantSandboxSpec{RuntimeClassName: sandboxClass, DatabaseRestarts: geassv1alpha1.DatabaseRestartsImmediate}))
}
