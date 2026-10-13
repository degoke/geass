package dashboard

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Project logs read what the nodes still hold: the container logs the kubelet keeps, through the
// Kubernetes API. Nothing is stored here, so a pod that is gone takes its history with it (the container
// before a restart is the exception, and is offered). Every limit below exists because this runs in the
// operator's own process: a busy project must not be able to use its memory or hold its connections.
const (
	// logTailDefault and logTailMax bound the lines read from each container.
	logTailDefault = 200
	logTailMax     = 1000
	// logBytesPerContainer bounds what one container can contribute, whatever its lines look like.
	logBytesPerContainer = 1 << 20
	// logLinesMax bounds a response: the newest lines of everything.
	logLinesMax = 3000
	// logMaxContainers bounds how many containers one request reads, or follows.
	logMaxContainers = 40
	// logReadConcurrency bounds how many containers are read at once.
	logReadConcurrency = 8
	// logReadTimeout bounds a whole read.
	logReadTimeout = 25 * time.Second
	// logLineMaxBytes is the longest line kept; a longer one is cut, not dropped.
	logLineMaxBytes = 16 * 1024
	// logStreamRescan is how often a followed project looks for pods that appeared since.
	logStreamRescan = 10 * time.Second
	// logSinceMax is the furthest back a request may ask. The node rarely has more.
	logSinceMax = 7 * 24 * time.Hour
)

// podLogsFunc opens one container's log. Tests replace it; nil reads from the Kubernetes API.
type podLogsFunc func(ctx context.Context, namespace, pod string, options *corev1.PodLogOptions) (io.ReadCloser, error)

// logsKey is the JSON key the log endpoints return their lines under.
const logsKey = "lines"

const (
	levelError = "error"
	levelWarn  = "warn"
	levelInfo  = "info"
	levelDebug = "debug"
)

// logLine is one line of output, with where it came from.
type logLine struct {
	Time      time.Time `json:"t"`
	Service   string    `json:"service"`
	Pod       string    `json:"pod"`
	Container string    `json:"container"`
	Text      string    `json:"line"`
	Level     string    `json:"level,omitempty"`
	// Previous marks output from before the container's last restart.
	Previous bool `json:"previous,omitempty"`
}

type logSource struct {
	namespace, pod, container, service string
	restarts                           int32
}

type logQuery struct {
	environment string
	service     string
	search      string
	level       string
	since       time.Duration
	tail        int64
	previous    bool
}

func parseLogQuery(r *http.Request) (logQuery, error) {
	values := r.URL.Query()
	query := logQuery{
		environment: strings.TrimSpace(values.Get("environment")),
		service:     strings.TrimSpace(values.Get("service")),
		search:      strings.TrimSpace(values.Get("q")),
		level:       strings.ToLower(strings.TrimSpace(values.Get("level"))),
		tail:        logTailDefault,
	}
	if wanted, err := strconv.ParseBool(values.Get("previous")); err == nil {
		query.previous = wanted
	}
	if len(query.search) > 200 {
		return query, platform.UserError("The search text is too long")
	}
	if !slices.Contains([]string{"", levelError, levelWarn, levelInfo, levelDebug}, query.level) {
		return query, platform.UserError("Level must be error, warn, info or debug")
	}
	if raw := strings.TrimSpace(values.Get("since")); raw != "" {
		since, err := time.ParseDuration(raw)
		if err != nil || since <= 0 || since > logSinceMax {
			return query, platform.UserError("Since must be a duration such as 15m, 1h or 24h, up to 7 days")
		}
		query.since = since
	}
	if raw := strings.TrimSpace(values.Get("tail")); raw != "" {
		tail, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || tail < 1 || tail > logTailMax {
			return query, platform.UserError(fmt.Sprintf("Tail must be a whole number from 1 to %d", logTailMax))
		}
		query.tail = tail
	}
	return query, nil
}

// levelPatterns classify a line by the words logs use, because logs have no common format. They are checked
// from the most to the least severe, so "error: retrying (warning)" is an error.
var levelPatterns = []struct {
	level   string
	pattern *regexp.Regexp
}{
	{levelError, regexp.MustCompile(`(?i)(\blevel["=:\s]+"?(error|err|fatal|panic|critical)\b|\[(error|err|fatal|panic|critical)\]|\b(error|fatal|panic|exception|traceback)\b)`)},
	{levelWarn, regexp.MustCompile(`(?i)(\blevel["=:\s]+"?warn(ing)?\b|\[warn(ing)?\]|\bwarn(ing)?\b)`)},
	{levelDebug, regexp.MustCompile(`(?i)(\blevel["=:\s]+"?(debug|trace)\b|\[(debug|trace)\]|\b(debug|trace)\b)`)},
}

// explicitLevel is the level structured logs state for themselves: {"level":"info",...} or level=info.
var explicitLevel = regexp.MustCompile(`(?i)(?:"(?:level|severity|lvl)"\s*:\s*"|\b(?:level|severity|lvl)=)"?([a-z]+)`)

// statedLevels maps the words structured logs use for their level onto ours. Info-like words map to "" (info).
var statedLevels = map[string]string{
	"error": levelError, "err": levelError, "fatal": levelError, "panic": levelError, "critical": levelError, "crit": levelError, "alert": levelError, "emerg": levelError,
	"warn": levelWarn, "warning": levelWarn,
	"debug": levelDebug, "trace": levelDebug,
	"info": "", "notice": "", "information": "",
}

func detectLevel(text string) string {
	// A level the line states outright beats guessing from its words: a CloudNativePG line that says
	// "level":"info" and mentions an error in its message is still information.
	if match := explicitLevel.FindStringSubmatch(text); match != nil {
		if level, known := statedLevels[strings.ToLower(match[1])]; known {
			return level
		}
	}
	for _, candidate := range levelPatterns {
		if candidate.pattern.MatchString(text) {
			return candidate.level
		}
	}
	return ""
}

// levelRank orders levels so that asking for "warn" also shows errors.
var levelRank = map[string]int{levelDebug: 0, levelInfo: 1, "": 1, levelWarn: 2, levelError: 3}

func (q logQuery) keeps(line logLine) bool {
	if q.level != "" {
		want := levelRank[q.level]
		// Lines with no recognisable level are treated as info, so they show unless errors are asked for.
		if levelRank[line.Level] < want {
			return false
		}
	}
	if q.search != "" && !strings.Contains(strings.ToLower(line.Text), strings.ToLower(q.search)) {
		return false
	}
	return true
}

// parseLogLine splits the timestamp the API puts in front of each line when asked to.
func parseLogLine(raw string) (time.Time, string) {
	stamp, text, found := strings.Cut(raw, " ")
	if !found {
		return time.Time{}, raw
	}
	parsed, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return time.Time{}, raw
	}
	return parsed, text
}

// projectLogSources finds the containers to read: every container of every pod in the project's
// environments, optionally limited to one environment or service. Pods are only ever taken from the
// project's own namespaces; nothing in the request names a namespace or a pod.
func (s *Server) projectLogSources(ctx context.Context, projectName string, query logQuery) (sources []logSource, services []string, err error) {
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(ctx, client.ObjectKey{Name: projectName, Namespace: systemNamespace}, &project); err != nil {
		return nil, nil, err
	}
	namespaces, err := projectMetricNamespaces(&project, query.environment)
	if err != nil {
		return nil, nil, platform.UserError("That environment is not part of this project")
	}
	owners, err := s.projectResourceOwners(ctx, projectName)
	if err != nil {
		return nil, nil, err
	}
	known := map[string]bool{}
	for _, namespace := range namespaces {
		var pods corev1.PodList
		if err := s.Client.List(ctx, &pods, client.InNamespace(namespace)); err != nil {
			return nil, nil, err
		}
		for i := range pods.Items {
			pod := &pods.Items[i]
			service := owners.service(pod)
			if service == "" {
				continue
			}
			known[service] = true
			if query.service != "" && query.service != service {
				continue
			}
			restarts := map[string]int32{}
			for _, status := range pod.Status.ContainerStatuses {
				restarts[status.Name] = status.RestartCount
			}
			for _, container := range pod.Spec.Containers {
				sources = append(sources, logSource{namespace: namespace, pod: pod.Name, container: container.Name, service: service, restarts: restarts[container.Name]})
			}
		}
	}
	services = make([]string, 0, len(known))
	for name := range known {
		services = append(services, name)
	}
	slices.Sort(services)
	if query.service != "" && !known[query.service] {
		return nil, services, nil
	}
	slices.SortFunc(sources, func(a, b logSource) int {
		return strings.Compare(a.namespace+"/"+a.pod+"/"+a.container, b.namespace+"/"+b.pod+"/"+b.container)
	})
	return sources, services, nil
}

// resourceOwners attributes a pod to the Geass resource it belongs to.
type resourceOwners struct {
	apps      map[string]bool
	databases map[string]bool
	// charts maps a Helm release name to the database that installed it.
	charts map[string]string
}

func (o resourceOwners) service(pod *corev1.Pod) string {
	labels := pod.Labels
	if name := labels[platform.K8sLabelAppName]; name != "" && (o.apps[name] || o.databases[name]) {
		return name
	}
	if name := labels["cnpg.io/cluster"]; name != "" && o.databases[name] {
		return name
	}
	if db, ok := o.charts[labels["app.kubernetes.io/instance"]]; ok {
		return db
	}
	return ""
}

func (s *Server) projectResourceOwners(ctx context.Context, project string) (resourceOwners, error) {
	owners := resourceOwners{apps: map[string]bool{}, databases: map[string]bool{}, charts: map[string]string{}}
	var apps geassv1alpha1.GeassAppList
	if err := s.Client.List(ctx, &apps, client.InNamespace(systemNamespace)); err != nil {
		return owners, err
	}
	for i := range apps.Items {
		if apps.Items[i].Spec.Project == project {
			owners.apps[apps.Items[i].Name] = true
		}
	}
	var databases geassv1alpha1.GeassDatabaseList
	if err := s.Client.List(ctx, &databases, client.InNamespace(systemNamespace)); err != nil {
		return owners, err
	}
	for i := range databases.Items {
		db := &databases.Items[i]
		if db.Spec.Project != project {
			continue
		}
		owners.databases[db.Name] = true
		owners.charts[platform.MySQLChartReleaseName(db.Name)] = db.Name
		owners.charts[platform.RedisChartReleaseName(db.Name)] = db.Name
	}
	return owners, nil
}

// openPodLog opens a container's log, through the replacement in tests or the Kubernetes API.
func (s *Server) openPodLog(ctx context.Context, namespace, pod string, options *corev1.PodLogOptions) (io.ReadCloser, error) {
	if s.podLogs != nil {
		return s.podLogs(ctx, namespace, pod, options)
	}
	if s.Kube == nil {
		return nil, fmt.Errorf("the dashboard has no cluster connection")
	}
	return s.Kube.CoreV1().Pods(namespace).GetLogs(pod, options).Stream(ctx)
}

// logOptions are the options for reading one container. Timestamps are always requested: merging the
// output of several containers needs them.
func (q logQuery) logOptions(source logSource, previous bool) *corev1.PodLogOptions {
	options := &corev1.PodLogOptions{
		Container:  source.container,
		Timestamps: true,
		TailLines:  &q.tail,
		Previous:   previous,
	}
	limit := int64(logBytesPerContainer)
	options.LimitBytes = &limit
	if q.since > 0 {
		seconds := int64(q.since.Seconds())
		options.SinceSeconds = &seconds
	}
	return options
}

// readContainerLog reads one container's log into lines. A container with nothing to read (not started,
// no previous run) yields none and no error: that is normal, not a failure of the request.
func (s *Server) readContainerLog(ctx context.Context, source logSource, query logQuery, previous bool) []logLine {
	stream, err := s.openPodLog(ctx, source.namespace, source.pod, query.logOptions(source, previous))
	if err != nil {
		return nil
	}
	defer func() { _ = stream.Close() }()
	var lines []logLine
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 64*1024), logLineMaxBytes*2)
	for scanner.Scan() {
		when, text := parseLogLine(scanner.Text())
		if len(text) > logLineMaxBytes {
			text = text[:logLineMaxBytes]
		}
		lines = append(lines, logLine{Time: when, Service: source.service, Pod: source.pod, Container: source.container, Text: text, Level: detectLevel(text), Previous: previous})
	}
	return lines
}

// readProjectLogs reads every selected container, a few at a time, and merges the lines by time.
func (s *Server) readProjectLogs(ctx context.Context, sources []logSource, query logQuery) (lines []logLine, truncated bool) {
	ctx, cancel := context.WithTimeout(ctx, logReadTimeout)
	defer cancel()
	if len(sources) > logMaxContainers {
		sources, truncated = sources[:logMaxContainers], true
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, logReadConcurrency)
	for _, source := range sources {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			read := s.readContainerLog(ctx, source, query, false)
			// The run before a restart is what explains a crash loop or an out-of-memory kill.
			if query.previous && source.restarts > 0 {
				read = append(read, s.readContainerLog(ctx, source, query, true)...)
			}
			mu.Lock()
			lines = append(lines, read...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	filtered := lines[:0]
	for _, line := range lines {
		if query.keeps(line) {
			filtered = append(filtered, line)
		}
	}
	slices.SortStableFunc(filtered, func(a, b logLine) int { return a.Time.Compare(b.Time) })
	if len(filtered) > logLinesMax {
		filtered, truncated = filtered[len(filtered)-logLinesMax:], true
	}
	return filtered, truncated
}

// handleAPIProjectLogs serves GET /api/projects/{name}/logs.
func (s *Server) handleAPIProjectLogs(w http.ResponseWriter, r *http.Request, project string) {
	s.writeProjectLogs(w, r, project, "")
}

// writeProjectLogs answers a project's recent logs. With forService set the answer is that one service's: its name
// is the whole list of services (the project's other services, and its databases, are not the caller's to learn
// from a service's logs), and the tail counts the newest lines across the service's copies, where for a project
// it is read from the end of each container's log.
func (s *Server) writeProjectLogs(w http.ResponseWriter, r *http.Request, project, forService string) {
	query, err := parseLogQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: err.Error()})
		return
	}
	sources, services, err := s.projectLogSources(r.Context(), project, query)
	if err != nil {
		writeLogSourceError(w, err)
		return
	}
	lines, truncated := s.readProjectLogs(r.Context(), sources, query)
	if lines == nil {
		lines = []logLine{}
	}
	if forService != "" {
		services = []string{forService}
		if int64(len(lines)) > query.tail {
			lines = lines[int64(len(lines))-query.tail:]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		logsKey: lines, "services": services, "truncated": truncated,
		"note": "These are the logs the nodes still hold. Output from pods that have been replaced or deleted is not kept.",
	})
}

func writeLogSourceError(w http.ResponseWriter, err error) {
	if platform.IsUserError(err) {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: err.Error()})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "project not found"})
}

// handleAPIProjectLogStream serves GET /api/projects/{name}/logs/stream as server-sent events. It sends
// the recent lines first, then follows every container, picking up pods that start later.
func (s *Server) handleAPIProjectLogStream(w http.ResponseWriter, r *http.Request, project string) {
	query, err := parseLogQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: err.Error()})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "streaming is not supported here"})
		return
	}
	sources, _, err := s.projectLogSources(r.Context(), project, query)
	if err != nil {
		writeLogSourceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()
	// Lines from every container funnel through one writer. The channel is bounded: a browser that cannot
	// keep up loses lines instead of making the operator hold them.
	out := make(chan logLine, 256)
	followed := map[string]bool{}
	var wg sync.WaitGroup
	follow := func(source logSource, first bool) {
		key := source.namespace + "/" + source.pod + "/" + source.container
		if followed[key] || len(followed) >= logMaxContainers {
			return
		}
		followed[key] = true
		wg.Go(func() { s.followContainer(ctx, source, query, first, out) })
	}
	for _, source := range sources {
		follow(source, true)
	}

	rescan := time.NewTicker(logStreamRescan)
	defer rescan.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case line := <-out:
			if !query.keeps(line) {
				continue
			}
			payload, err := json.Marshal(line)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		case <-rescan.C:
			fresh, _, err := s.projectLogSources(ctx, project, query)
			if err != nil {
				continue
			}
			for _, source := range fresh {
				follow(source, false)
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// followContainer streams one container's log into out until the stream ends or the request does. A
// container that was there when the stream opened starts with its recent tail; one that appears later
// starts from its first line.
func (s *Server) followContainer(ctx context.Context, source logSource, query logQuery, recent bool, out chan<- logLine) {
	options := query.logOptions(source, false)
	options.Follow = true
	options.LimitBytes = nil
	if !recent {
		options.TailLines = nil
	}
	stream, err := s.openPodLog(ctx, source.namespace, source.pod, options)
	if err != nil {
		return
	}
	defer func() { _ = stream.Close() }()
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 64*1024), logLineMaxBytes*2)
	for scanner.Scan() {
		when, text := parseLogLine(scanner.Text())
		if len(text) > logLineMaxBytes {
			text = text[:logLineMaxBytes]
		}
		line := logLine{Time: when, Service: source.service, Pod: source.pod, Container: source.container, Text: text, Level: detectLevel(text)}
		select {
		case out <- line:
		case <-ctx.Done():
			return
		default:
			// The reader is behind; drop this line rather than pile them up.
		}
	}
}
