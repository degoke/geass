package metering

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestReadGivesEveryHourOfTheRangeAndTheOnesNeverWrittenAsEmpty(t *testing.T) {
	// The range crosses midnight, so the hours are read from two days.
	stored := []time.Time{at("2026-10-22T22:00:00Z"), at("2026-10-23T01:00:00Z")}
	c := newClient(hourConfigMap(t, stored[0]), hourConfigMap(t, stored[1]), hourConfigMap(t, at("2026-10-23T05:00:00Z")))

	hours, err := Read(context.Background(), c, at("2026-10-22T22:30:00Z"), at("2026-10-23T02:00:00Z"))

	require.NoError(t, err)
	require.Len(t, hours, 4, "22:00 (the start is taken back to its hour), 23:00, 00:00 and 01:00")
	for i, hour := range hours {
		require.Equal(t, at("2026-10-22T22:00:00Z").Add(time.Duration(i)*time.Hour), hour.Start)
	}
	require.Equal(t, int64(3600), hours[0].MeteredSeconds)
	require.Zero(t, hours[1].MeteredSeconds, "an hour that was never written is there, with nothing metered")
	require.Zero(t, hours[2].MeteredSeconds)
	require.Equal(t, int64(3600), hours[3].MeteredSeconds)
}

func TestReadRefusesMoreThanMaxHoursAndAnHourItCannotRead(t *testing.T) {
	from := at("2026-09-01T00:00:00Z")
	_, err := Read(context.Background(), newClient(), from, from.Add((MaxHours+1)*time.Hour))
	require.ErrorContains(t, err, "at most 744 hours")
	hours, err := Read(context.Background(), newClient(), from, from.Add(MaxHours*time.Hour))
	require.NoError(t, err)
	require.Len(t, hours, MaxHours)

	broken := hourConfigMap(t, from)
	broken.Data[dataKey] = `{"version":1,"start":"2026-09-01T00:00:00Z","coveredUntil":"2026-09-01T03:00:00Z"}`
	_, err = Read(context.Background(), newClient(broken), from, from.Add(time.Hour))
	require.ErrorContains(t, err, "outside its hour", "an hour that cannot be read is an error, never an empty hour")
}

func TestAnHourIsClosedOnceMeteredToItsEndOrSoonAfterItEnds(t *testing.T) {
	start := at("2026-10-23T10:00:00Z")
	partial := Hour{Start: start, CoveredUntil: at("2026-10-23T10:40:00Z"), MeteredSeconds: 2400}
	require.False(t, partial.Closed(at("2026-10-23T10:50:00Z")))
	require.False(t, partial.Closed(at("2026-10-23T11:04:59Z")), "a sample can still add to it until MaxStep after its end")
	require.True(t, partial.Closed(at("2026-10-23T11:05:00Z")))
	full := Hour{Start: start, CoveredUntil: start.Add(time.Hour), MeteredSeconds: 3600}
	require.True(t, full.Closed(at("2026-10-23T11:00:00Z")))
	require.True(t, Hour{Start: start}.Closed(at("2026-10-23T11:05:00Z")), "an hour nothing was written in closes as well")
}

func TestAnHourIsKeptInAConfigMapNamedAndLabelledForItsHour(t *testing.T) {
	start := at("2026-10-23T09:00:00Z")
	configMap := &corev1.ConfigMap{}
	require.NoError(t, encode(Hour{Start: start, CoveredUntil: start.Add(time.Minute), MeteredSeconds: 60,
		Orgs: map[string]map[string]Amounts{"acme": {"": {CPUMilliSeconds: 6000}}}}, configMap))

	require.Equal(t, "geass-usage-2026102309", configMap.Name)
	require.Equal(t, "geass-system", configMap.Namespace)
	require.Equal(t, "2026102309", configMap.Labels["geass.dev/usage-hour"])
	require.Equal(t, "20261023", configMap.Labels["geass.dev/usage-day"])
	hour, err := decode(configMap)
	require.NoError(t, err)
	require.Equal(t, int64(6000), hour.Orgs["acme"][""].CPUMilliSeconds)
	require.Equal(t, formatVersion, hour.Version)

	configMap.Name = "geass-usage-2026102310"
	_, err = decode(configMap)
	require.ErrorContains(t, err, "does not hold the hour its name says")
}
