package platform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEveryFunctionRuntimeHasAnImageAndAMountedEntrypoint(t *testing.T) {
	runtimes := FunctionRuntimes()
	require.Len(t, runtimes, 3)
	for _, runtime := range runtimes {
		require.NotEmpty(t, runtime.Image, runtime.ID)
		require.NotEmpty(t, runtime.File, runtime.ID)
		require.Contains(t, strings.Join(runtime.Command, " "), FunctionMountPath+"/"+runtime.File,
			"%s must run the file it mounts", runtime.ID)
		require.NoError(t, ValidateFunction(runtime.ID, FunctionStarterCode(runtime.ID)), "the starter code must be deployable")
	}
}

func TestStarterCodeServesOnThePortFromTheEnvironment(t *testing.T) {
	for _, runtime := range FunctionRuntimes() {
		require.Contains(t, FunctionStarterCode(runtime.ID), "PORT", runtime.ID)
	}
}

func TestValidateFunction(t *testing.T) {
	require.Error(t, ValidateFunction("ruby", "puts 1"), "unknown runtimes are rejected")
	require.Error(t, ValidateFunction("bun", ""), "code is required")
	require.Error(t, ValidateFunction("bun", strings.Repeat("a", MaxFunctionCodeBytes+1)), "oversized code is rejected")
	require.NoError(t, ValidateFunction("python", "print('ok')"))
}

// The last-deployed snapshot embeds the code JSON-escaped in an annotation, and Kubernetes caps all
// annotations at 256 KiB, so the limit has to be measured on the escaped size.
func TestValidateFunctionLeavesRoomForTheDeploySnapshot(t *testing.T) {
	require.NoError(t, ValidateFunction("bun", strings.Repeat("a", 190*1024)))

	quotes := strings.Repeat(`"`, 120*1024) // 120 KB raw, 240 KB once escaped
	require.Less(t, len(quotes), MaxFunctionCodeBytes, "under the raw limit")
	require.Error(t, ValidateFunction("bun", quotes), "too big once escaped")

	escaped, err := json.Marshal(strings.Repeat("a", 190*1024))
	require.NoError(t, err)
	require.LessOrEqual(t, len(escaped), MaxFunctionSnapshotBytes)
	require.Less(t, MaxFunctionSnapshotBytes, 256*1024, "room is left for the rest of the spec")
}

func TestFunctionCodeHashChangesWithCodeAndRuntime(t *testing.T) {
	base := FunctionCodeHash("bun", "a")
	require.Equal(t, base, FunctionCodeHash("bun", "a"))
	require.NotEqual(t, base, FunctionCodeHash("bun", "b"))
	require.NotEqual(t, base, FunctionCodeHash("node", "a"))
}
