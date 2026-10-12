package platform

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func TestTwoProjectsCanBothAskForTheSameDatabaseName(t *testing.T) {
	dbA, loginA := SharedDatabaseNames("proj-aaaa1111", "production", "app")
	dbB, loginB := SharedDatabaseNames("proj-bbbb2222", "production", "app")

	require.NotEqual(t, dbA, dbB, "one server, two projects, one name each")
	require.NotEqual(t, loginA, loginB)
	require.Equal(t, "proj_aaaa1111_production_app", dbA)
}

func TestTheSameInputsAlwaysGiveTheSameNames(t *testing.T) {
	db1, login1 := SharedDatabaseNames("shop", "production", "orders")
	db2, login2 := SharedDatabaseNames("shop", "production", "orders")
	require.Equal(t, db1, db2, "the operator recomputes them on every pass, and a different answer would orphan the database")
	require.Equal(t, login1, login2)
}

func TestEnvironmentsOfTheSameProjectGetTheirOwnDatabases(t *testing.T) {
	prod, loginProd := SharedDatabaseNames("shop", "production", "orders")
	staging, loginStaging := SharedDatabaseNames("shop", "staging", "orders")
	require.NotEqual(t, prod, staging)
	require.NotEqual(t, loginProd, loginStaging, "staging's login must not open production's data")
}

func TestNamesThatDifferOnlyInPunctuationDoNotShareALogin(t *testing.T) {
	_, a := SharedDatabaseNames("a-b", "production", "c")
	_, b := SharedDatabaseNames("a_b", "production", "c")
	require.NotEqual(t, a, b, "both read as a_b_production_c in the database name, but logins come from the raw names")
}

func TestLoginsFitEveryEnginesLimit(t *testing.T) {
	_, login := SharedDatabaseNames(strings.Repeat("p", 50), strings.Repeat("e", 50), strings.Repeat("n", 50))
	require.LessOrEqual(t, len(login), 32, "MySQL allows 32 characters")
	require.Regexp(t, `^u_[0-9a-f]+$`, login, "a login says nothing about its project")
}

func TestLongNamesAreShortenedWithoutCollidingAndStayLegal(t *testing.T) {
	long := strings.Repeat("x", 80)
	db1, _ := SharedDatabaseNames("shop", "production", long+"1")
	db2, _ := SharedDatabaseNames("shop", "production", long+"2")

	require.LessOrEqual(t, len(db1), maxSharedDatabaseName)
	require.NotEqual(t, db1, db2, "two names that share a long prefix are still two databases")
	require.Regexp(t, `^[a-z_][a-z0-9_]*$`, db1)
}

func TestNamesAreSafeToUseUnquoted(t *testing.T) {
	legal := regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
	for _, project := range []string{"proj-1", "Shop", "9lives", "a.b", "x y", "", "ünïcode", "drop;--"} {
		database, login := SharedDatabaseNames(project, "production", "app")
		require.Regexp(t, legal, database, project)
		require.Regexp(t, legal, login, project)
		require.NotContains(t, database, ";", project)
	}
}

func TestADatabaseWithNoProjectIsTheSharedServer(t *testing.T) {
	require.True(t, IsSharedDatabase(&geassv1alpha1.GeassDatabase{}))
	require.True(t, IsSharedDatabase(&geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{Project: "  "}}))
	require.False(t, IsSharedDatabase(&geassv1alpha1.GeassDatabase{ObjectMeta: metav1.ObjectMeta{Name: "x"}, Spec: geassv1alpha1.GeassDatabaseSpec{Project: "shop"}}))
}
