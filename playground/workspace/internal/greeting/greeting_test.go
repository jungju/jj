package greeting

import "testing"

func TestMessage(t *testing.T) {
	if got := Message(); got != "Hello, world!" {
		t.Fatalf("Message() = %q", got)
	}
}
