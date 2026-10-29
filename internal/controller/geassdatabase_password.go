package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A database Geass runs is made with a login whose password Geass makes, once, in the database's bootstrap Secret. Until
// this release the password was the database's name followed by "-password", which anything that could reach the
// database could guess. It is now random (generatedPasswordBytes of it, as hex, so it is safe in a connection address and in
// a shell). A database that already has its Secret keeps the password in it: it is what the server was started with.
//
// The one case that cannot be random is a database restored from a physical copy, which holds the logins of the server
// it was taken from: the copy's password has to be the new database's. It travels with the archive instead (see
// ensureArchiveCredentials).

const generatedPasswordBytes = 24

// generateDatabasePassword is a password nobody can guess.
func generateDatabasePassword() (string, error) {
	raw := make([]byte, generatedPasswordBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("could not make a password: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// newBootstrapPassword is the password for a database whose Secret is being made: a random one, or for a database that
// is restored from a physical copy the one the copy carries.
func (r *GeassDatabaseReconciler) newBootstrapPassword(ctx context.Context, db *geassv1alpha1.GeassDatabase) (string, error) {
	if origin := db.Annotations[annotationCredentialsOrigin]; origin != "" {
		return r.restoredPassword(ctx, db, origin)
	}
	return generateDatabasePassword()
}

// restoredPassword is the password of the server a physical copy was taken from. The archive carries it. An archive from
// before passwords were random does not, and its server's password was derived from its name.
func (r *GeassDatabaseReconciler) restoredPassword(ctx context.Context, db *geassv1alpha1.GeassDatabase, origin string) (string, error) {
	if restore := db.Spec.RestoreFrom; restore != nil {
		secret := &corev1.Secret{}
		err := r.Get(ctx, client.ObjectKey{Name: platform.ArchiveCredentialsSecretName(restore.Archive), Namespace: platform.SystemNamespace}, secret)
		switch {
		case err == nil:
			if password := secretValue(secret, platform.ArchiveCredentialsKeyPassword); password != "" {
				return password, nil
			}
		case !apierrors.IsNotFound(err):
			return "", err
		}
	}
	return origin + "-password", nil
}

// ensureArchiveCredentials keeps the password of a database that is backed up with physical copies in a Secret that goes
// with its archive, and so lives as long as its backups do, which is longer than the database. A database restored from
// those copies cannot start without it.
func (r *GeassDatabaseReconciler) ensureArchiveCredentials(ctx context.Context, db *geassv1alpha1.GeassDatabase, archive *geassv1alpha1.GeassBackupArchive, wsNS string) error {
	if archive == nil || platform.LayoutOf(archive.Spec.Source) != platform.LayoutPhysical {
		return nil
	}
	bootstrap := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: db.Name + "-bootstrap", Namespace: wsNS}, bootstrap); err != nil {
		// The database's own Secret is made a moment later; the next look finds it.
		return client.IgnoreNotFound(err)
	}
	password := secretValue(bootstrap, "password")
	if password == "" {
		return nil
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: platform.ArchiveCredentialsSecretName(archive.Name), Namespace: platform.SystemNamespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels[platform.LabelManagedBy] = platform.ManagedByValue
		setSecretStrings(secret, map[string]string{platform.ArchiveCredentialsKeyPassword: password})
		return controllerutil.SetControllerReference(archive, secret, r.Scheme)
	})
	return err
}
