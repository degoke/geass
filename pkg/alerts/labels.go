package alerts

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// The labels of the problem metrics and so of the alerts made from them. Routes match on them and templates
// read them, so they are named once.
const (
	// MetricProblem is the gauge the operator exports for each thing that is wrong. Its value is 1.
	MetricProblem = "geass_problem"
	// MetricProblemsEvaluated is the time the operator last looked for problems. It is there only while what the operator says
	// can be believed, which is what the alerts that depend on the other metrics wait for.
	MetricProblemsEvaluated = "geass_problems_evaluated_timestamp_seconds"
	// MetricProjectNamespace maps a namespace to the project and environment that own it, so that alerts made from
	// Kubernetes metrics (a pod that crashes, a volume that fills) can be said to belong to a project.
	MetricProjectNamespace = "geass_project_namespace_info"

	// LabelPullRequest is PullRequestYes on the namespace of an environment that was made for a pull request.
	LabelPullRequest = "pull_request"
	PullRequestYes   = "true"

	LabelKind         = "geass_kind"
	LabelSeverity     = "severity"
	LabelProject      = "project"
	LabelProjectName  = "project_name"
	LabelEnvironment  = "environment"
	LabelResource     = "resource"
	LabelResourceKind = "resource_kind"
	// LabelPath is where in the dashboard to look, such as /projects/proj-1/apps/web.
	LabelPath = "path"
	// LabelDetail says which of several problems of one kind this is, where the kind has more than one.
	LabelDetail = "detail"
	// LabelTestChannel marks a test alert and names the one channel it is for.
	LabelTestChannel = "geass_test_channel"
	// LabelTestID makes each test alert its own. Alertmanager holds an alert group for a while after its alerts end, and
	// a new alert that joins an existing group waits for the group's next interval, which is minutes, instead of being
	// sent at once. A second test of the same channel would otherwise wait for, or be taken for, the first.
	LabelTestID = "geass_test_id"

	// KindTest is the kind of a test alert.
	KindTest = "test"

	maxLabelLength = 120
)

// SafeLabel makes a name fit to be a label of an alert, and so to be put in a Slack message or an email. A service's
// display name is whatever its owner typed, and Slack reads <!channel> in a message as a call to the whole
// workspace and & and < as markup, and a line break in an email's subject starts another header. It keeps
// printable text, drops the characters that mean something to Slack, and cuts a long name short.
func SafeLabel(value string) string {
	var out strings.Builder
	for _, r := range strings.ToValidUTF8(value, "") {
		switch {
		case r == '<' || r == '>' || r == '&' || r == '"' || r == '`' || r == '{' || r == '}':
			continue
		case unicode.IsControl(r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r):
			out.WriteRune(' ')
		default:
			out.WriteRune(r)
		}
	}
	cleaned := strings.Join(strings.Fields(out.String()), " ")
	if utf8.RuneCountInString(cleaned) <= maxLabelLength {
		return cleaned
	}
	return string([]rune(cleaned)[:maxLabelLength-1]) + "…"
}
