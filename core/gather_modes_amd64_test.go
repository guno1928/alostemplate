//go:build amd64 && !purego

package core

import "testing"

func forEachGatherMode(t *testing.T, run func(t *testing.T)) {
	original := gatherUseAVX2
	t.Cleanup(func() { gatherUseAVX2 = original })
	modes := []bool{false}
	if original {
		modes = append(modes, true)
	}
	for _, useAVX2 := range modes {
		name := "sse"
		if useAVX2 {
			name = "avx2"
		}
		gatherUseAVX2 = useAVX2
		t.Run(name, run)
	}
}
