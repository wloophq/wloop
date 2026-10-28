package source_test

import "testing"

func TestAPluginsCallsAreMeteredUnderTheHostsScope(t *testing.T) {
	t.Skip("carried from X7 review M5: the host sets no Call meter scope on its requests and Services.Meter answers ErrUnsupported (X8a2)")
}
