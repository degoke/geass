package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

type deploymentLogSection struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Lines string `json:"lines"`
}

func parseDeploymentLogsPath(path string) (kind, resourceName, deploymentID string, ok bool) {
	path = strings.TrimPrefix(strings.Trim(path, "/"), "/")
	parts := strings.Split(path, "/")
	// api / apps / name / deployments / id / logs
	if len(parts) != 6 || parts[0] != "api" || parts[3] != "deployments" || parts[5] != "logs" {
		return "", "", "", false
	}
	switch parts[1] {
	case "apps", "object-stores", "databases", "logical-databases":
		return parts[1], parts[2], parts[4], true
	default:
		return "", "", "", false
	}
}

func (s *Server) handleAPIDeploymentLogs(w http.ResponseWriter, r *http.Request) {
	kind, name, deploymentID, ok := parseDeploymentLogsPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	var sections []deploymentLogSection

	switch kind {
	case "apps":
		var app geassv1alpha1.GeassApp
		if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &app); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "app not found"})
			return
		}
		attempt, dep := s.resolveAppDeployment(ctx, &app, deploymentID)
		sections = append(sections, provisionLogSection(attempt))
		if attempt != nil && attempt.BuildRef != "" {
			sections = append(sections, s.buildLogSection(ctx, attempt.BuildRef))
		} else if dep != nil && dep.Spec.BuildRef != "" {
			sections = append(sections, s.buildLogSection(ctx, dep.Spec.BuildRef))
		}
		sections = append(sections, s.appDeployLogSection(ctx, &app, dep, attempt))
	case "object-stores":
		var store geassv1alpha1.GeassObjectStore
		if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &store); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "object store not found"})
			return
		}
		attempt := findProvisionAttempt(store.Status.Provision, deploymentID)
		sections = append(sections, provisionLogSection(attempt))
	case "databases":
		var db geassv1alpha1.GeassDatabase
		if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &db); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "database not found"})
			return
		}
		attempt := findProvisionAttempt(db.Status.Provision, deploymentID)
		sections = append(sections, provisionLogSection(attempt))
	case "logical-databases":
		var ldb geassv1alpha1.GeassLogicalDatabase
		if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &ldb); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "logical database not found"})
			return
		}
		attempt := findProvisionAttempt(ldb.Status.Provision, deploymentID)
		sections = append(sections, provisionLogSection(attempt))
	default:
		http.NotFound(w, r)
		return
	}

	filtered := make([]deploymentLogSection, 0, len(sections))
	for _, section := range sections {
		if strings.TrimSpace(section.Lines) != "" {
			filtered = append(filtered, section)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       deploymentID,
		"sections": filtered,
	})
}

func findProvisionAttempt(status geassv1alpha1.GeassProvisionStatus, id string) *geassv1alpha1.GeassProvisionAttempt {
	for i := range status.ProvisionAttempts {
		if status.ProvisionAttempts[i].ID == id {
			return &status.ProvisionAttempts[i]
		}
		if status.ProvisionAttempts[i].DeploymentRef == id {
			return &status.ProvisionAttempts[i]
		}
	}
	return nil
}

func (s *Server) resolveAppDeployment(ctx context.Context, app *geassv1alpha1.GeassApp, id string) (*geassv1alpha1.GeassProvisionAttempt, *geassv1alpha1.GeassDeployment) {
	attempt := findProvisionAttempt(app.Status.Provision, id)
	var dep geassv1alpha1.GeassDeployment
	depKey := client.ObjectKey{Name: id, Namespace: systemNamespace}
	if err := s.Client.Get(ctx, depKey, &dep); err == nil && dep.Spec.App == app.Name {
		if attempt == nil {
			attempt = findProvisionAttempt(app.Status.Provision, dep.Name)
			if attempt == nil {
				for i := range app.Status.Provision.ProvisionAttempts {
					if app.Status.Provision.ProvisionAttempts[i].DeploymentRef == dep.Name {
						attempt = &app.Status.Provision.ProvisionAttempts[i]
						break
					}
				}
			}
		}
		return attempt, &dep
	}
	if attempt != nil {
		if attempt.DeploymentRef != "" {
			if err := s.Client.Get(ctx, client.ObjectKey{Name: attempt.DeploymentRef, Namespace: systemNamespace}, &dep); err == nil {
				return attempt, &dep
			}
		}
		return attempt, nil
	}
	return nil, nil
}

func provisionLogSection(attempt *geassv1alpha1.GeassProvisionAttempt) deploymentLogSection {
	section := deploymentLogSection{ID: "provision", Title: "Provision"}
	if attempt == nil {
		section.Lines = "No provision log was recorded for this deployment."
		return section
	}
	var b strings.Builder
	if attempt.Summary != "" {
		fmt.Fprintf(&b, "%s\n", attempt.Summary)
	}
	for _, errLine := range attempt.Errors {
		when := ""
		if !errLine.RecordedAt.IsZero() {
			when = errLine.RecordedAt.Format(time.RFC3339) + " "
		}
		fmt.Fprintf(&b, "%s%s\n", when, errLine.Message)
	}
	if b.Len() == 0 {
		switch attempt.Phase {
		case geassv1alpha1.ProvisionAttemptRunning:
			b.WriteString("Provisioning in progress…\n")
		case geassv1alpha1.ProvisionAttemptSucceeded:
			b.WriteString("Provisioning completed successfully.\n")
		default:
			b.WriteString("No provision messages for this deployment.\n")
		}
	}
	section.Lines = strings.TrimRight(b.String(), "\n")
	return section
}

func (s *Server) buildLogSection(ctx context.Context, buildName string) deploymentLogSection {
	section := deploymentLogSection{ID: "build", Title: "Build"}
	if buildName == "" {
		return section
	}
	var build geassv1alpha1.GeassBuild
	if err := s.Client.Get(ctx, client.ObjectKey{Name: buildName, Namespace: systemNamespace}, &build); err != nil {
		section.Lines = "Build record not found."
		return section
	}
	if build.Status.FailureReason != "" {
		section.Lines = build.Status.FailureReason
	}
	if build.Status.LastLogLine != "" {
		if section.Lines != "" {
			section.Lines += "\n"
		}
		section.Lines += build.Status.LastLogLine
	}
	if s.Kube != nil {
		if output := s.buildPodOutput(ctx, &build); output != "" {
			section.Lines = output
		}
	}
	if section.Lines == "" {
		section.Lines = fmt.Sprintf("Build %s (%s) — logs appear while the build runs or after it finishes.", buildName, build.Status.Phase)
	}
	return section
}

// buildPodOutput is the log of the pod that ran a build. The pods of a Job are named <job>-<random suffix>, so
// they are found by the label every pod of a Job carries and not by a name. A Job retries a pod that failed,
// so the newest pod is the last attempt. Its kaniko container has the build; when that never started because
// the clone failed, the output of the git container is what says why.
func (s *Server) buildPodOutput(ctx context.Context, build *geassv1alpha1.GeassBuild) string {
	pods, err := s.Kube.CoreV1().Pods(buildJobNamespace(build)).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + build.Name + "-kaniko"})
	if err != nil || len(pods.Items) == 0 {
		return ""
	}
	slices.SortFunc(pods.Items, func(a, b corev1.Pod) int {
		if c := b.CreationTimestamp.Compare(a.CreationTimestamp.Time); c != 0 {
			return c
		}
		return strings.Compare(b.Name, a.Name)
	})
	for _, container := range []string{"kaniko", buildCloneContainer} {
		data, err := s.Kube.CoreV1().Pods(buildJobNamespace(build)).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{Container: container}).Do(ctx).Raw()
		if err == nil && len(data) > 0 {
			return string(data)
		}
	}
	return ""
}

func (s *Server) appDeployLogSection(ctx context.Context, app *geassv1alpha1.GeassApp, dep *geassv1alpha1.GeassDeployment, attempt *geassv1alpha1.GeassProvisionAttempt) deploymentLogSection {
	section := deploymentLogSection{ID: "deploy", Title: "Deploy"}
	if s.Kube == nil {
		section.Lines = "Deploy logs are unavailable until the dashboard has a cluster connection."
		return section
	}
	latestDep := s.latestAppDeploymentName(ctx, app.Name)
	selected := ""
	if dep != nil {
		selected = dep.Name
	} else if attempt != nil && attempt.DeploymentRef != "" {
		selected = attempt.DeploymentRef
	}
	if selected != "" && latestDep != "" && selected != latestDep {
		section.Lines = "Deploy logs are only kept for the active deployment. Select the current deployment to stream live service logs."
		return section
	}
	ns, err := resourceNamespaceForApp(*app)
	if err != nil {
		section.Lines = "Could not load deploy logs."
		return section
	}
	pods, err := s.Kube.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=" + app.Name})
	if err != nil || len(pods.Items) == 0 {
		section.Lines = "No running pods yet. Deploy logs appear after the service is live."
		return section
	}
	var out strings.Builder
	for _, pod := range pods.Items {
		result := s.Kube.CoreV1().Pods(ns).GetLogs(pod.Name, &corev1.PodLogOptions{Container: "app", TailLines: int64ptr(400)}).Do(ctx)
		data, readErr := result.Raw()
		fmt.Fprintf(&out, "# %s\n", pod.Name)
		if readErr != nil {
			out.WriteString("could not load logs for this pod\n")
			continue
		}
		out.Write(data)
		if !strings.HasSuffix(out.String(), "\n") {
			out.WriteString("\n")
		}
	}
	section.Lines = strings.TrimRight(out.String(), "\n")
	return section
}

func (s *Server) latestAppDeploymentName(ctx context.Context, appName string) string {
	var list geassv1alpha1.GeassDeploymentList
	if err := s.Client.List(ctx, &list, client.InNamespace(systemNamespace), client.MatchingLabels{platform.LabelApp: appName}); err != nil || len(list.Items) == 0 {
		return ""
	}
	latest := list.Items[0]
	for i := range list.Items {
		if list.Items[i].CreationTimestamp.After(latest.CreationTimestamp.Time) {
			latest = list.Items[i]
		}
	}
	return latest.Name
}
