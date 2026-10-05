package capability

import (
	"slices"
	"strings"
	"testing"
)

func TestCanonicalContract(t *testing.T) {
	got, err := Normalize([]string{" GPU ", "cpu", "CPU", "ffmpeg.v2", "a_b-1"})
	if err != nil || !slices.Equal(got, []string{"a_b-1", "cpu", "ffmpeg.v2", "gpu"}) || !Canonical(got) {
		t.Fatal(got, err)
	}
	for _, values := range [][]string{{""}, {" "}, {"\tcpu"}, {"cpu\n"}, {"a/b"}, {"-gpu"}, {"é"}, {"K"}, {strings.Repeat("a", 33)}, make([]string, 17)} {
		if _, err := Normalize(values); err == nil {
			t.Fatalf("accepted %q", values)
		}
	}
	for _, raw := range []string{"cpu,,gpu", ",cpu", "cpu,", "\n"} {
		if _, err := ParseConfig(raw); err == nil {
			t.Fatalf("accepted config %q", raw)
		}
	}
	got, err = ParseConfig("CPU,gpu, cpu ")
	if err != nil || !slices.Equal(got, []string{"cpu", "gpu"}) {
		t.Fatal(got, err)
	}
	empty, err := Normalize(nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatal(empty, err)
	}
	if Canonical([]string{"gpu", "cpu"}) || Canonical([]string{"cpu", "cpu"}) || Canonical([]string{"CPU"}) {
		t.Fatal("noncanonical stored set")
	}
}
