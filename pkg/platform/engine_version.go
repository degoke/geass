package platform

import (
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// EngineVersions are the versions of the engines that Geass picks the version of, as they are shown: the release line of the
// image Geass runs, "8.4" for 8.4.11. Postgres is not here, because a Postgres database says which version it runs. The
// version field of a database is defaulted to a Postgres version, and means nothing for the others, so what it holds is
// not what is run.
func EngineVersions() map[string]string {
	return map[string]string{
		string(geassv1alpha1.DatabaseEngineMySQL):   releaseLine(MySQLImage),
		string(geassv1alpha1.DatabaseEngineMariaDB): releaseLine(MariaDBImage),
		string(geassv1alpha1.DatabaseEngineRedis):   releaseLine(RedisImage),
		// SQLite databases are served by rqlite, whose version is the one that is pinned.
		string(geassv1alpha1.DatabaseEngineSQLite): "rqlite " + releaseLine(SQLiteImage),
	}
}

// DatabaseVersion is the version a database runs, as it is shown: the version it asks for when it is a Postgres database,
// and the one Geass runs for the other engines. It is empty when there is none to show.
func DatabaseVersion(db *geassv1alpha1.GeassDatabase) string {
	if db.Spec.Engine == geassv1alpha1.DatabaseEnginePostgres {
		return db.Spec.Version
	}
	return EngineVersions()[string(db.Spec.Engine)]
}

// releaseLine is the major and minor version in an image's tag: "8.4" for "mysql:8.4.11", and for "redis:7.4.11-alpine" "7.4".
func releaseLine(image string) string {
	tag := image[strings.LastIndex(image, ":")+1:]
	parts := strings.SplitN(tag, ".", 3)
	if len(parts) < 2 {
		return tag
	}
	return parts[0] + "." + parts[1]
}
