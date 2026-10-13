package installer

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/degoke/geass/pkg/migrations"
	"github.com/degoke/geass/pkg/release"
)

// OperatorTakeover waits until the operator that was just installed has taken over, and rolls the operator back to the
// one that ran before when it has not.
//
// Taking over is two things: the Deployment rolls out, and the operator says on the cluster object that it is the release
// that was installed (status.release), which it does when it starts to reconcile. A rollout that finished only says that
// some operator is running; the report says it is the new one, and that it got past its migrations.
//
// Rolling back is what an update that cannot take over should end in, and not a cluster with no operator: the old operator
// was stopped to make room for the new one (see config/manager/manager.yaml), so a new one that cannot start leaves nothing
// reconciling. It is only done when it is safe, which is not always:
//
//   - The custom resource definitions only grow, and a migration adds the new shape beside the old one, so a previous
//     operator can read everything a new one that never ran left behind. But one that migrated and then failed has
//     moved the data to a schema level that the previous operator may refuse (or, if it predates the record, misread).
//     The schema record says which: past the level the previous operator reported (or the baseline), there is no rollback.
//   - A new operator that did take over (it reported its release) may have written fields that the previous one does not
//     know, and an operator that writes an object back drops what it does not know. That one is not rolled back.
//   - A run that did not replace the operator has nothing to roll back to: `rollout undo` would go to an older revision
//     than the one that was running.
type OperatorTakeover struct {
	Plan *UpgradePlan
	// RolloutTimeout and ReleaseTimeout bound the two waits (defaultRolloutTimeout and five minutes when zero), and
	// RollbackTimeout the wait for the previous operator to start again after a rollback (three minutes when zero).
	RolloutTimeout, ReleaseTimeout, RollbackTimeout time.Duration
}

// defaultRolloutTimeout is how long the new operator has to become ready. It has to outlast what the pod itself is given:
// the operator runs its data migrations before it is ready, and config/manager/manager.yaml lets its startupProbe wait
// for them for 12 minutes (periodSeconds 5 x failureThreshold 144). An installer that gave up sooner would roll back an
// operator that is healthy and part way through a migration. A pod that is crash looping or cannot pull its image is
// noticed long before this (see operatorPodProblem), so the longer wait only costs time when something is slow.
const defaultRolloutTimeout = 15 * time.Minute

func (s *OperatorTakeover) Name() string { return "wait for the new operator to take over" }

func orDuration(d, fallback time.Duration) time.Duration {
	if d == 0 {
		return fallback
	}
	return d
}

func (s *OperatorTakeover) Run() error {
	cause := waitForOperatorRollout(s.Name(), operatorDeployment, orDuration(s.RolloutTimeout, defaultRolloutTimeout))
	if cause == nil {
		cause = s.waitForRelease(orDuration(s.ReleaseTimeout, 5*time.Minute))
	}
	if cause == nil {
		return nil
	}
	return s.failed(cause)
}

// waitForOperatorRollout waits for the Deployment to finish rolling out. It gives up early when the new pod is crash
// looping or cannot pull its image, which five more minutes of waiting would not change.
func waitForOperatorRollout(step, deployment string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	Logf(step, "Waiting for deployment/%s rollout until %s", deployment, deadline.UTC().Format(time.RFC3339))
	for {
		_, err := kubectl(step, nil, "rollout", "status", "deployment/"+deployment, "-n", geassNamespace, "--timeout=15s")
		if err == nil {
			Logf(step, "Operator rollout completed")
			return nil
		}
		if problem := operatorPodProblem(step); problem != "" {
			return fmt.Errorf("the operator pod is not starting: %s", problem)
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the operator did not become ready within %s", timeout)
		}
		fmt.Printf("  waiting... %s\n", strings.TrimSpace(err.Error()))
		time.Sleep(pollInterval)
	}
}

// operatorPodProblem says why an operator pod will not start, or "" when it is as slow as a start can be. Only the pods
// that are not being deleted count: the one that was replaced is on its way out.
func operatorPodProblem(step string) string {
	pods, ok, err := kubeGetList(step, "pods", "-n", geassNamespace, "-l", "control-plane=controller-manager")
	if err != nil || !ok {
		return ""
	}
	for _, raw := range pods {
		var pod struct {
			Metadata kubeMeta `json:"metadata"`
			Status   struct {
				ContainerStatuses []struct {
					Name         string `json:"name"`
					RestartCount int    `json:"restartCount"`
					State        struct {
						Waiting *struct {
							Reason  string `json:"reason"`
							Message string `json:"message"`
						} `json:"waiting"`
					} `json:"state"`
				} `json:"containerStatuses"`
			} `json:"status"`
		}
		if json.Unmarshal(raw, &pod) != nil || pod.Metadata.DeletionTimestamp != "" {
			continue
		}
		for _, c := range pod.Status.ContainerStatuses {
			if c.State.Waiting == nil {
				continue
			}
			switch c.State.Waiting.Reason {
			case "ImagePullBackOff", "ErrImagePull", "InvalidImageName", "CreateContainerConfigError", "CreateContainerError":
				return fmt.Sprintf("pod %s: %s %s", pod.Metadata.Name, c.State.Waiting.Reason, c.State.Waiting.Message)
			case "CrashLoopBackOff":
				if c.RestartCount >= 2 {
					return fmt.Sprintf("pod %s has crashed %d times (%s)", pod.Metadata.Name, c.RestartCount, c.State.Waiting.Reason)
				}
			}
		}
	}
	return ""
}

// waitForRelease waits until the cluster object says that the operator is the release that was installed.
func (s *OperatorTakeover) waitForRelease(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var seen string
	for {
		seen = s.reportedRelease()
		if seen == s.Plan.Target {
			Logf(s.Name(), "The operator reports %s", seen)
			fmt.Printf("  operator is running %s\n", release.Describe(seen))
			return nil
		}
		if problem := operatorPodProblem(s.Name()); problem != "" {
			return fmt.Errorf("the operator pod is not starting: %s", problem)
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the new operator did not report release %s within %s (the cluster says %s)",
				s.Plan.Target, timeout, release.Describe(seen))
		}
		fmt.Printf("  waiting... the cluster reports %s\n", release.Describe(seen))
		time.Sleep(pollInterval)
	}
}

func (s *OperatorTakeover) reportedRelease() string {
	out, err := kubectl(s.Name(), nil, "-n", geassNamespace, "get", "geasscluster", s.Plan.State.ClusterName, "-o", "jsonpath={.status.release}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// failed is what happens when the new operator did not take over: say why, and go back if that is safe.
func (s *OperatorTakeover) failed(cause error) error {
	step, plan := s.Name(), s.Plan
	// The evidence first: a rollback replaces the pod that holds it.
	evidence := operatorEvidence(step)
	fmt.Printf("  the new operator did not take over: %v\n", cause)

	if why := s.noRollbackBecause(); why != "" {
		return fmt.Errorf("%v. The operator was not rolled back: %s.\n%s\n%s", cause, why, s.whatToDo(false), evidence)
	}

	previous := release.Describe(plan.State.RecordedRelease())
	fmt.Printf("  rolling the operator back to %s (%s)\n", previous, orUnknown(plan.State.OperatorImage))
	if err := kubectlEcho(step, nil, "-n", geassNamespace, "rollout", "undo", "deployment/"+operatorDeployment); err != nil {
		return fmt.Errorf("%v. The installer tried to roll the operator back and could not: %w.\n%s\n%s", cause, err, s.whatToDo(false), evidence)
	}
	if err := waitForOperatorRollout(step, operatorDeployment, orDuration(s.RollbackTimeout, 3*time.Minute)); err != nil {
		return fmt.Errorf("%v. The installer rolled the operator back, and the previous one did not start either: %w. "+
			"Its image may have been removed from K3s's containerd by its cleanup of unused images.\n%s\n%s", cause, err, s.whatToDo(false), evidence)
	}
	var now ClusterState
	if err := inspectOperator(step, &now); err == nil && plan.State.OperatorImage != "" && now.OperatorImage != plan.State.OperatorImage {
		Warnf(step, "The operator now runs %s, not the %s it ran before the update", now.OperatorImage, plan.State.OperatorImage)
	}
	return fmt.Errorf("%v. The installer rolled the operator back to %s (%s), which is running again, and went no further.\n%s\n%s",
		cause, previous, orUnknown(plan.State.OperatorImage), s.whatToDo(true), evidence)
}

// whatToDo says what state the cluster is left in, and how to go on.
func (s *OperatorTakeover) whatToDo(rolledBack bool) string {
	plan := s.Plan
	var b strings.Builder
	if rolledBack {
		b.WriteString("What the update changed and kept: the custom resource definitions (they only grow, so the previous operator reads everything)")
	} else {
		b.WriteString("What the update changed: the custom resource definitions and the operator's permissions")
	}
	if plan.K3sTo != "" && plan.State.K3sVersion != plan.K3sTo {
		fmt.Fprintf(&b, ", and K3s on the control plane (%s, which does not go back)", plan.K3sTo)
	}
	b.WriteString(". The cluster object, spec.k3s.version and the workers were not touched.")
	if plan.SnapshotName != "" {
		fmt.Fprintf(&b, " The snapshot taken first is %s: restoring it undoes all of it (see Go back in the upgrade guide).", plan.SnapshotDescription())
	}
	if rolledBack {
		b.WriteString(" Read why the new operator failed below, fix it or install a release that fits, and run the installer again.")
	} else {
		b.WriteString(" Read why the new operator failed below; either fix it and let it start, install a release that fits the data, or restore the snapshot.")
	}
	return b.String()
}

// noRollbackBecause says why the operator must not be rolled back, or "" when it may be.
func (s *OperatorTakeover) noRollbackBecause() string {
	step, plan := s.Name(), s.Plan
	if plan.Options.NoRollback {
		return "--no-rollback was passed"
	}
	if s.reportedRelease() == plan.Target {
		return "the new operator is running, so it may already have written fields that the previous one does not know, and an operator that " +
			"writes an object back drops what it does not know"
	}
	var now ClusterState
	if err := inspectOperator(step, &now); err != nil {
		return "the Deployment could not be read: " + err.Error()
	}
	if now.OperatorImage == plan.State.OperatorImage && now.OperatorImageDigest == plan.State.OperatorImageDigest &&
		now.OperatorRelease == plan.State.OperatorRelease {
		return "this run did not replace the operator, so there is no earlier one to go back to (rolling back would go to an older revision still)"
	}
	level, err := schemaLevel(step)
	if err != nil {
		return "the data's schema level could not be read (" + err.Error() + "), so it cannot be said that the previous operator can read it"
	}
	safe := max(plan.State.SchemaVersion, migrations.BaselineLevel)
	if level > safe {
		return fmt.Sprintf("the new operator already moved the data to schema level %d, and the previous one only knows up to level %d: "+
			"it would refuse it, or misread it if it predates the record", level, safe)
	}
	return ""
}

// schemaLevel is the level the cluster's data is at, from the record that the operator keeps. No record is level 0.
func schemaLevel(step string) (int, error) {
	out, err := kubectl(step, nil, "-n", geassNamespace, "get", "configmap", migrations.ConfigMapName, "-o", "jsonpath={.data.schemaVersion}")
	if err != nil {
		if strings.Contains(err.Error(), "(NotFound)") {
			return 0, nil
		}
		return 0, err
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return 0, nil
	}
	level, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("the record holds %q", text)
	}
	return level, nil
}

// operatorEvidence is what says why an operator is not running: its pods, and the end of its log, current and previous.
func operatorEvidence(step string) string {
	var b strings.Builder
	if out, err := kubectl(step, nil, "-n", geassNamespace, "get", "pods", "-l", "control-plane=controller-manager"); err == nil {
		b.WriteString("The operator's pods:\n" + string(out))
	}
	for _, previous := range []bool{false, true} {
		args := []string{"-n", geassNamespace, "logs", "deployment/" + operatorDeployment, "-c", "manager", "--tail=25"}
		label := "The operator's log says:"
		if previous {
			args = append(args, "--previous")
			label = "The log of the container before it restarted says:"
		}
		if out, err := kubectl(step, nil, args...); err == nil && strings.TrimSpace(string(out)) != "" {
			b.WriteString(label + "\n" + string(out))
		}
	}
	if b.Len() == 0 {
		return "The operator's log could not be read: kubectl -n geass-system logs deployment/" + operatorDeployment + " -c manager"
	}
	return b.String()
}
