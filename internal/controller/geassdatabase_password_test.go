package controller

import (
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

var generatedPassword = regexp.MustCompile(`^[0-9a-f]{48}$`)

// requireGeneratedPassword says a password is one Geass made at random, and not the database's name with "-password"
// after it, which was the password until anything that could reach a database could guess it.
func requireGeneratedPassword(t *testing.T, database, got, message string) {
	t.Helper()
	require.Regexp(t, generatedPassword, got, message)
	require.NotEqual(t, database+"-password", got, message)
}

func TestTheLoginOfANewDatabaseHasARandomPasswordThatIsKept(t *testing.T) {
	ctx := context.Background()
	first, second := newBackupEnv(t), newBackupEnv(t)

	require.NoError(t, first.r.reconcileBootstrapSecret(ctx, first.db, first.wsNS))
	require.NoError(t, second.r.reconcileBootstrapSecret(ctx, second.db, second.wsNS))
	one := string(first.secret(first.wsNS, testDBName+"-bootstrap").Data["password"])
	other := string(second.secret(second.wsNS, testDBName+"-bootstrap").Data["password"])

	requireGeneratedPassword(t, testDBName, one, "")
	require.NotEqual(t, one, other, "two databases of the same name, in two clusters, do not share a password")
	require.Equal(t, "app", string(first.secret(first.wsNS, testDBName+"-bootstrap").Data["username"]))

	// It is made once: the server was started with it.
	require.NoError(t, first.r.reconcileBootstrapSecret(ctx, first.db, first.wsNS))
	require.Equal(t, one, string(first.secret(first.wsNS, testDBName+"-bootstrap").Data["password"]))
}

// A database that was made before passwords were random has its password in its Secret, and the server was started with
// it: it is the one thing that must not change under it.
func TestADatabaseThatAlreadyHasItsPasswordKeepsIt(t *testing.T) {
	ctx := context.Background()
	env := newBackupEnv(t, func(c *backupEnvConfig) {
		c.extraObjects = append(c.extraObjects, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: testDBName + "-bootstrap", Namespace: testDevTargetNS},
			Data:       map[string][]byte{"username": []byte("app"), "password": []byte(testDBName + "-password")},
		})
	})
	require.NoError(t, env.r.reconcileBootstrapSecret(ctx, env.db, env.wsNS))
	require.Equal(t, testDBName+"-password", string(env.secret(env.wsNS, testDBName+"-bootstrap").Data["password"]))
}

func TestAConnectionIsNotMadeWithoutAPassword(t *testing.T) {
	env := newBackupEnv(t, func(c *backupEnvConfig) {
		c.extraObjects = append(c.extraObjects, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: testDBName + "-bootstrap", Namespace: testDevTargetNS}})
	})
	err := env.r.reconcileGeneratedConnectionSecret(context.Background(), env.db, env.wsNS, "host", "5432", "postgresql")
	require.Error(t, err, "a made-up password would be one the server does not have")
}

func physicalArchive(name string) *geassv1alpha1.GeassBackupArchive {
	return &geassv1alpha1.GeassBackupArchive{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassBackupArchiveSpec{
			Project: testProjectName, Database: "books", ServerName: name,
			Source: geassv1alpha1.GeassBackupSource{Engine: geassv1alpha1.DatabaseEngineMariaDB, HighAvailability: true},
		},
	}
}

// The copy a replicated MariaDB database writes holds its logins. A database restored from it cannot start without the
// password of the server it came from, which now is random, so it is kept beside the archive and so lives as long as the
// backups do: longer than the database.
func TestTheArchiveOfAPhysicalBackupKeepsThePasswordOfItsDatabase(t *testing.T) {
	ctx := context.Background()
	env := newBackupEnv(t, func(c *backupEnvConfig) {
		c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) { db.Spec.Engine = geassv1alpha1.DatabaseEngineMariaDB })
		c.extraObjects = append(c.extraObjects, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: testDBName + "-bootstrap", Namespace: testDevTargetNS},
			Data:       map[string][]byte{"password": []byte("the-servers-password")},
		})
	})
	archive := physicalArchive("books-1a2b3c4d")
	require.NoError(t, env.client.Create(ctx, archive))

	require.NoError(t, env.r.ensureArchiveCredentials(ctx, env.db, archive, env.wsNS))

	kept := env.secret(platform.SystemNamespace, platform.ArchiveCredentialsSecretName(archive.Name))
	require.Equal(t, "the-servers-password", string(kept.Data[platform.ArchiveCredentialsKeyPassword]))
	require.NotEmpty(t, kept.OwnerReferences, "it goes when the archive does")

	// A database with another kind of backup keeps no password anywhere else.
	snapshot := physicalArchive("orders-9f8e7d6c")
	snapshot.Spec.Source.HighAvailability = false
	require.NoError(t, env.client.Create(ctx, snapshot))
	require.NoError(t, env.r.ensureArchiveCredentials(ctx, env.db, snapshot, env.wsNS))
	require.False(t, env.exists(&corev1.Secret{}, platform.SystemNamespace, platform.ArchiveCredentialsSecretName(snapshot.Name)))
}

func TestARestoredDatabaseStartsWithThePasswordTheArchiveKeeps(t *testing.T) {
	ctx := context.Background()
	source := replicatedSource("20261011T020000Z")
	source.Spec.Database = "books"
	env := mariaRestoreEnv(t, source, geassv1alpha1.GeassDatabaseRestoreSpec{}, true)
	require.NoError(t, env.client.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platform.ArchiveCredentialsSecretName(source.Name), Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{platform.ArchiveCredentialsKeyPassword: []byte("random-password-of-the-source")},
	}))
	require.NoError(t, env.r.inheritMariaDBCredentials(ctx, env.db))

	require.NoError(t, env.r.reconcileBootstrapSecret(ctx, env.db, env.wsNS))

	require.Equal(t, "random-password-of-the-source", string(env.secret(env.wsNS, testDBName+"-bootstrap").Data["password"]))
}

func TestAnArchiveOfADeletedDatabaseWithNoPasswordFallsBackToTheOldDerivation(t *testing.T) {
	env := newBackupEnv(t, func(c *backupEnvConfig) {
		c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) {
			db.Spec.RestoreFrom = &geassv1alpha1.GeassDatabaseRestoreSpec{Archive: "gone"}
		})
	})
	password, err := env.r.restoredPassword(context.Background(), env.db, "books")
	require.NoError(t, err)
	require.Equal(t, "books-password", password, "an archive from before passwords were random")
}
