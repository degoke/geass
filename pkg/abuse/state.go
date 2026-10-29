package abuse

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

const (
	// StateName is the ConfigMap of the system namespace that the sustained-CPU signal keeps its runs in.
	StateName = "geass-abuse-signal"
	stateKey  = "state.json"
)

// State is what the sustained-CPU signal keeps between samples: the runs of the services at the threshold now, by the
// service's name, and whether its sources answered the last time it looked.
type State struct {
	// Sampled is when it last looked.
	Sampled time.Time `json:"sampled,omitzero"`
	// CPU says whether the metrics server answered.
	CPU Availability `json:"cpu"`
	// Traffic says whether Prometheus gave the requests the services were sent. It is nil while the settings do not ask.
	Traffic *Availability  `json:"traffic,omitempty"`
	Runs    map[string]Run `json:"runs,omitempty"`
}

// Availability is whether a source answered, and why not.
type Availability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// LoadState reads the state and the ConfigMap it is in, which is nil when there is none yet. A state that cannot be read
// is taken as none: the runs start again, which costs a window and loses nothing else.
func LoadState(ctx context.Context, reader client.Reader) (State, *corev1.ConfigMap, error) {
	configMap := &corev1.ConfigMap{}
	err := reader.Get(ctx, client.ObjectKey{Name: StateName, Namespace: platform.SystemNamespace}, configMap)
	if apierrors.IsNotFound(err) {
		return State{}, nil, nil
	}
	if err != nil {
		return State{}, nil, err
	}
	var state State
	if raw := configMap.Data[stateKey]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			state = State{}
		}
	}
	return state, configMap, nil
}

// StoreState writes the state into the ConfigMap it was read from, or a new one. An update carries the version it was read
// at, so that two operators that both think they lead never interleave their samples; the one that loses tries again at the
// next sample. No more than MaxRuns runs are kept: those that began first.
func StoreState(ctx context.Context, c client.Client, state State, configMap *corev1.ConfigMap) error {
	state.Runs = bounded(state.Runs)
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if configMap == nil {
		configMap = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name: StateName, Namespace: platform.SystemNamespace,
			Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue},
		}}
		configMap.Data = map[string]string{stateKey: string(raw)}
		return c.Create(ctx, configMap)
	}
	if configMap.Data[stateKey] == string(raw) {
		return nil
	}
	configMap.Data = map[string]string{stateKey: string(raw)}
	if err := c.Update(ctx, configMap); err != nil {
		return fmt.Errorf("writing the abuse signal's state: %w", err)
	}
	return nil
}

// DeleteState removes the state, when the settings are taken away.
func DeleteState(ctx context.Context, c client.Client) error {
	err := c.Delete(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: StateName, Namespace: platform.SystemNamespace}})
	return client.IgnoreNotFound(err)
}

func bounded(runs map[string]Run) map[string]Run {
	if len(runs) <= MaxRuns {
		return runs
	}
	names := slices.Collect(maps.Keys(runs))
	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(runs[a].Since.Compare(runs[b].Since), cmp.Compare(a, b))
	})
	kept := make(map[string]Run, MaxRuns)
	for _, name := range names[:MaxRuns] {
		kept[name] = runs[name]
	}
	return kept
}
