package node

import "testing"

func TestEnvironmentNameOverridesConfig(t *testing.T) {
	SetName("configured")
	t.Cleanup(func() { SetName("") })
	t.Setenv("DROP_NAME", "temporary")

	if got := DisplayName(); got != "temporary" {
		t.Fatalf("display name = %q, want environment override", got)
	}
}

func TestConfiguredNameIsUsedWithoutAnOverride(t *testing.T) {
	SetName("configured")
	t.Cleanup(func() { SetName("") })
	t.Setenv("DROP_NAME", "")

	if got := DisplayName(); got != "configured" {
		t.Fatalf("display name = %q, want configured name", got)
	}
}
