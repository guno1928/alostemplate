//go:build !amd64 || purego

package core

import "testing"

func forEachGatherMode(t *testing.T, run func(t *testing.T)) {
	t.Run("generic", run)
}
