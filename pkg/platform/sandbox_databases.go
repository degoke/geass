package platform

import (
	"context"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// An organization's database server holds its data and runs what the organization sends it: a login with enough rights
// runs programs through it (Postgres's COPY ... PROGRAM and untrusted languages, a MySQL plugin or function loaded from a
// file), so with spec.tenancy.sandbox set it runs under the RuntimeClass as the organization's services do, and so do the
// Jobs that copy, restore and make databases on it. A server is an organization's when its project is, or, for a shared
// server, when its spec.org names one. The platform's servers (a shared server of no organization), which while
// organizations are on hold only the databases of projects of no organization, keep the node's own runtime like the rest
// of the platform's pods: no organization's code runs on them.
//
// Restarting a database is not rolling a service, so a server that already runs is moved into the sandbox (or out of it,
// or to another class) only when the platform says so (spec.tenancy.sandbox.databaseRestarts, or the
// AnnotationSandboxRestart on the server). The class a server's pods are written with is recorded on its GeassDatabase
// (AnnotationSandboxRuntimeClass), as its placement and storage class are, so a change of the setting alone never
// restarts it. An engine whose operator cannot give its pods a RuntimeClass is never run outside the sandbox for an
// organization instead: a new server of it is not made, and one that was made before is held as it is.

const (
	// AnnotationSandboxRuntimeClass on a GeassDatabase is the RuntimeClass its server's pods are written with, absent for the
	// node's own runtime. Geass sets it when the server is made and when it is restarted; it is not for editing.
	AnnotationSandboxRuntimeClass = "geass.dev/sandbox-runtime-class"
	// AnnotationSandboxRestart on a GeassDatabase approves one restart of its server into what the sandbox setting asks for
	// now. Any value approves. Geass removes it once the server's pods are written that way.
	AnnotationSandboxRestart = "geass.dev/sandbox-restart"

	// SandboxReasonEngineCannotBeSandboxed is why an organization's database server is not sandboxed: its engine runs on an
	// operator that cannot give its pods a RuntimeClass. Such a server is not made, or, made before, is held as it is.
	SandboxReasonEngineCannotBeSandboxed = "EngineCannotBeSandboxed"
	// SandboxReasonRestartPending is why a running database server's pods do not run as the setting asks: they are moved
	// only when the server is restarted, which waits for the platform's approval.
	SandboxReasonRestartPending = "RestartPending"

	// EventSandboxEngineUnsupported is the warning event of a server whose engine cannot be sandboxed, with the action
	// Refused when it is not made and Held when it was made before.
	EventSandboxEngineUnsupported = "SandboxEngineUnsupported"
	// EventSandboxRestartPending is the warning event of a server that begins to wait for a restart into what the setting
	// asks for.
	EventSandboxRestartPending = "SandboxRestartPending"
	// EventSandboxRestart is the event of a running server whose pods are written with another runtime, which restarts it.
	EventSandboxRestart = "SandboxRestart"

	// MessageCannotBeSandboxed is in every message of a server that is refused or held for its engine. Neither is counted
	// as a failed provision: only the platform can change it.
	MessageCannotBeSandboxed = "cannot run in the sandbox"
)

// ResolveDatabaseSandbox is how the pods of a database server, or of the Jobs that work on its data, run, given the node
// selector they are made with: as its project's pods do (ResolveSandbox), or for a shared server as its organization's do
// (ResolveOrganizationSandbox). The errors are theirs: a *SandboxRuntimeMissingError means the pods must not be made.
func ResolveDatabaseSandbox(ctx context.Context, c client.Reader, db *geassv1alpha1.GeassDatabase, nodeSelector map[string]string) (Sandbox, error) {
	if IsSharedDatabase(db) {
		return ResolveOrganizationSandbox(ctx, c, db.Spec.Org, nodeSelector)
	}
	return ResolveSandbox(ctx, c, db.Spec.Project, nodeSelector)
}

// EngineSandboxGap says why Geass cannot run an in-cluster server of the engine under a RuntimeClass, or nothing when it
// can. Geass writes the MySQL (single server), Redis and SQLite servers' pods itself, and MOCO, which runs a highly
// available MySQL, takes a whole pod spec; CloudNativePG and mariadb-operator have no field for it.
func EngineSandboxGap(engine geassv1alpha1.GeassDatabaseEngine) string {
	switch engine {
	case geassv1alpha1.DatabaseEnginePostgres, "":
		return "PostgreSQL runs on the CloudNativePG operator, and the release Geass installs (chart " + CNPGChartVersion +
			", CloudNativePG 1.25) has no way to give a server's pods a RuntimeClass"
	case geassv1alpha1.DatabaseEngineMariaDB:
		return "MariaDB runs on mariadb-operator, which has no way to give a server's pods a RuntimeClass"
	}
	return ""
}

// EngineCannotBeSandboxedMessage is what an organization's database server whose engine cannot run in the sandbox says:
// that it is not made, or, when it was made before, that it is held as it is.
func EngineCannotBeSandboxedMessage(gap string, made bool) string {
	if made {
		return fmt.Sprintf("This database %s: %s. It was made before, so it keeps running as it is, outside the sandbox, "+
			"but Geass changes nothing of it (no resize, no new settings or backups) while the platform runs organizations' "+
			"code in a sandbox. A platform administrator can move its data to an engine that runs in the sandbox, or turn the sandbox off",
			MessageCannotBeSandboxed, gap)
	}
	return fmt.Sprintf("This database %s: %s, and an organization's database server is not started outside the sandbox "+
		"that the platform runs organizations' code in, so it is not made. Choose MySQL, Redis or SQLite instead, or ask a "+
		"platform administrator", MessageCannotBeSandboxed, gap)
}

// SandboxRestartPendingMessage says how a running server's pods run (running, empty for the node's own runtime) until it
// is restarted as the setting asks (wanted), and how that restart is approved.
func SandboxRestartPendingMessage(running, wanted string) string {
	now := "Runs without the sandbox"
	if running != "" {
		now = "Runs under RuntimeClass " + running
	}
	next := "into RuntimeClass " + wanted
	if wanted == "" {
		next = "to run without it"
	}
	return fmt.Sprintf("%s until it is restarted %s, which restarts the database. A platform administrator annotates it %s "+
		"to restart it now, or sets spec.tenancy.sandbox.databaseRestarts to %s", now, next, AnnotationSandboxRestart,
		geassv1alpha1.DatabaseRestartsImmediate)
}

// DatabaseRestartsImmediately is whether a running server is restarted as the setting asks without waiting for approval.
func DatabaseRestartsImmediately(setting *geassv1alpha1.GeassTenantSandboxSpec) bool {
	return setting != nil && setting.DatabaseRestarts == geassv1alpha1.DatabaseRestartsImmediate
}

// DatabaseSandboxGap is why an in-cluster database's server, with its pods held to the node selector given, would have to
// run in the sandbox and cannot, or nothing when it need not or can: it is an organization's, the sandbox is on and asks
// for a RuntimeClass there, and its engine cannot run under one (EngineSandboxGap). A missing RuntimeClass is no reason:
// the server waits for it. It is what the dashboard refuses a new server, or a server given to an organization, for.
func DatabaseSandboxGap(ctx context.Context, c client.Reader, db *geassv1alpha1.GeassDatabase, nodeSelector map[string]string) (string, error) {
	if db.Spec.Placement == geassv1alpha1.DatabasePlacementExternal {
		return "", nil
	}
	gap := EngineSandboxGap(db.Spec.Engine)
	if gap == "" {
		return "", nil
	}
	sandbox, err := ResolveDatabaseSandbox(ctx, c, db, nodeSelector)
	var missing *SandboxRuntimeMissingError
	if err != nil && !errors.As(err, &missing) {
		return "", err
	}
	if sandbox.RuntimeClass == "" {
		return "", nil
	}
	return gap, nil
}

// EngineCannotBeGivenMessage is what refuses to give a shared server to an organization while its engine cannot run in the
// sandbox the organization's servers run in.
func EngineCannotBeGivenMessage(gap string) string {
	return fmt.Sprintf("This server %s: %s, and an organization's database server does not run outside the sandbox that "+
		"the platform runs organizations' code in, so it is not given to an organization while the sandbox is on", MessageCannotBeSandboxed, gap)
}
