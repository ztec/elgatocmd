package buildinfo

import "testing"

func TestDevelopmentVersionIsNotEmpty(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must always have a development fallback")
	}
}
