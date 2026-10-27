package metering

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

// MaxHours is the most hours one read returns: 31 days. A longer range is read in steps.
const MaxHours = 31 * 24

// Read returns every hour that starts at or after from and before to, oldest first. from is taken back to the start of
// its hour. An hour that was never written is there too, empty and with nothing metered, so that a gap is never silent.
// The hours are read a day at a time from what reader holds, which for the operator is the API server.
func Read(ctx context.Context, reader client.Reader, from, to time.Time) ([]Hour, error) {
	from, to = HourOf(from), to.UTC()
	if !from.Before(to) {
		return nil, nil
	}
	if hours := to.Sub(from).Hours(); hours > MaxHours {
		return nil, fmt.Errorf("at most %d hours can be read at once, and %.0f were asked for", MaxHours, hours)
	}
	stored := map[int64]Hour{}
	for day := from.Truncate(24 * time.Hour); day.Before(to); day = day.Add(24 * time.Hour) {
		var list corev1.ConfigMapList
		if err := reader.List(ctx, &list, client.InNamespace(platform.SystemNamespace), client.MatchingLabels{platform.LabelUsageDay: day.Format(dayLayout)}); err != nil {
			return nil, err
		}
		for i := range list.Items {
			if _, isHour := list.Items[i].Labels[platform.LabelUsageHour]; !isHour {
				continue
			}
			hour, err := decode(&list.Items[i])
			if err != nil {
				return nil, err
			}
			stored[hour.Start.Unix()] = hour
		}
	}
	hours := make([]Hour, 0, int(to.Sub(from).Hours())+1)
	for start := from; start.Before(to); start = start.Add(time.Hour) {
		hour, ok := stored[start.Unix()]
		if !ok {
			hour = Hour{Start: start}
		}
		hours = append(hours, hour)
	}
	return hours, nil
}
