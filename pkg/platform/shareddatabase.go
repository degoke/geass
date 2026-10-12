package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// IsSharedDatabase reports whether a database is the cluster-wide server: one with no project.
func IsSharedDatabase(db *geassv1alpha1.GeassDatabase) bool {
	return strings.TrimSpace(db.Spec.Project) == ""
}

// DefaultSharedConnectionLimit is how many connections a database's own login may hold on a shared
// server when nothing else is asked for.
const DefaultSharedConnectionLimit = 20

const (
	// maxSharedDatabaseName is the longest database name both Postgres (63) and MySQL (64) accept, with room.
	maxSharedDatabaseName = 60
	// sharedLoginBytes is how much of a digest names a login. Logins are short because MySQL allows 32 characters.
	sharedLoginBytes = 8
)

var unsafeNameChars = regexp.MustCompile(`[^a-z0-9]+`)

// SharedDatabaseNames are the database and login a logical database gets on the cluster-wide server.
//
// Many projects share one server, so the database is prefixed with its project and environment and two
// projects can both ask for "app". The login is a name derived from the same three parts, short enough
// for any engine and unguessable from the project's name; it owns that one database and nothing else.
func SharedDatabaseNames(project, environment, name string) (database, login string) {
	digest := sha256.Sum256([]byte(project + "\x00" + environment + "\x00" + name))
	login = "u_" + hex.EncodeToString(digest[:sharedLoginBytes])

	database = strings.Join([]string{identifierPart(project), identifierPart(environment), identifierPart(name)}, "_")
	// An identifier cannot start with a digit unquoted, and project names (DNS labels) may.
	if database[0] >= '0' && database[0] <= '9' {
		database = "d" + database
	}
	if len(database) > maxSharedDatabaseName {
		// Shortened names would collide, so the digest keeps them apart.
		database = database[:maxSharedDatabaseName-9] + "_" + hex.EncodeToString(digest[:4])
	}
	return database, login
}

// identifierPart lowercases a name into letters, digits and underscores, which any engine accepts unquoted.
func identifierPart(part string) string {
	cleaned := strings.Trim(unsafeNameChars.ReplaceAllString(strings.ToLower(part), "_"), "_")
	if cleaned == "" {
		return "x"
	}
	return cleaned
}
