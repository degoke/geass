// Package testshapes holds answers of the Geass API in the shape the dashboard really sends them, for the stub
// servers in the tests of the clients (the command line, the MCP server and pkg/client). A stub written beside
// the client it serves tends to agree with that client's mistakes: both once read {"logs": [...]} where the API
// answers {"lines": [...]}. Keeping one definition here, which the dashboard's own tests compare with what its
// handlers answer, means a change to the API has one place to be followed.
package testshapes

// LogsAnswer is GET /api/v1/apps/{name}/logs: the lines under "lines", one entry each (t, service, pod,
// container, line, level), beside the name of the one service and a note.
func LogsAnswer(service string, lines ...map[string]any) map[string]any {
	if lines == nil {
		lines = []map[string]any{}
	}
	return map[string]any{
		"lines": lines, "services": []string{service}, "truncated": false,
		"note": "These are the logs the nodes still hold. Output from pods that have been replaced or deleted is not kept.",
	}
}

// SleepStatus is GET /api/v1/apps/{name}/status for a service that asked to sleep when idle, once the operator has
// looked: active, or not with the reason.
func SleepStatus(name string, active bool, message string) map[string]any {
	status := SleepUnanswered(name)
	status["scaleToZero"] = map[string]any{"idleSeconds": 600, "maxReplicas": 3, "active": active, "message": message}
	return status
}

// SleepUnanswered is the same for a service whose request to sleep the operator has not looked at yet: nothing
// says whether it is active.
func SleepUnanswered(name string) map[string]any {
	return map[string]any{
		"name": name, "project": "shop", "environment": "production", "state": "ready", "ready": true, "pendingChanges": 0,
		"scaleToZero": map[string]any{"idleSeconds": 600, "maxReplicas": 3},
	}
}

// NoSleep is GET /api/v1/apps/{name}/status for a service that never asked to sleep: it has no scaleToZero.
func NoSleep(name string) map[string]any {
	return map[string]any{"name": name, "project": "shop", "environment": "production", "state": "ready", "ready": true, "pendingChanges": 0}
}
