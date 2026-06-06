package assert

import (
	"strings"
	"testing"
)

func assertFailure() {
	Assert(false)
}

func TestAssertReportsCallerLocation(t *testing.T) {
	var got string

	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic")
			}

			msg, ok := r.(string)
			if !ok {
				t.Fatalf("expected panic string, got %T", r)
			}

			got = msg
		}()

		assertFailure()
	}()

	if !strings.Contains(got, "src/assert/assert_test.go:") {
		t.Fatalf("expected panic to report assert test file, got %q", got)
	}

	if !strings.Contains(got, "github.com/envoyproxy/ratelimit/src/assert.assertFailure") {
		t.Fatalf("expected panic to report assertFailure function, got %q", got)
	}

	if strings.Contains(got, "TestAssertReportsCallerLocation") {
		t.Fatalf("expected panic to report immediate caller instead of outer test frame, got %q", got)
	}
}
