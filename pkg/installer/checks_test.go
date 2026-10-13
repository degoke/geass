package installer

import (
	"strings"
	"testing"
)

// MemTotal on machines sold as 2 GB, as the kernel reports it in kB.
func TestAServerSoldAsTwoGigabytesPassesTheMemoryCheck(t *testing.T) {
	for name, memKB := range map[string]int{
		"a full 2 GiB":                   2097152,
		"a typical 2 GiB cloud VM":       2007316,
		"a 2 GiB VM with a crash kernel": 1790000,
		"2 GB in decimal, less overhead": 1830000,
		"the least that passes":          1700 * 1024,
	} {
		if err := checkMemory(memKB); err != nil {
			t.Errorf("%s (%d kB) was refused: %v", name, memKB, err)
		}
	}
}

func TestAServerWithTooLittleMemoryIsRefusedWithTheFigures(t *testing.T) {
	err := checkMemory(960 * 1024) // what a 1 GiB machine reports
	if err == nil {
		t.Fatal("a 1 GiB machine must not pass")
	}
	for _, want := range []string{"960 MB", "1700"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
	if checkMemory(minMemoryMiB*1024-1) == nil || checkMemory(minMemoryMiB*1024) != nil {
		t.Error("the threshold is exactly minMemoryMiB")
	}
}
