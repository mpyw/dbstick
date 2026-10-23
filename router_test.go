package dbstick

import "testing"

func TestNewRouterPanicsOnNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("did not panic")
		}
	}()
	NewRouter[string](nil, "primary", "replica")
}
