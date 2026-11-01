package scenario

import (
	"testing"

	"github.com/aviciot/them-test-runner/them"
)

func TestApplyExpect(t *testing.T) {
	cases := []struct {
		name   string
		expect string
		in     them.UserResult
		want   string
	}{
		{"default keeps passed", "", them.UserResult{Status: "passed"}, "passed"},
		{"default keeps failed", "allow", them.UserResult{Status: "failed", HTTPStatus: 403}, "failed"},
		{"deny + 403 passes", "deny", them.UserResult{Status: "failed", HTTPStatus: 403, Error: "connect: bad handshake"}, "passed"},
		{"deny + admitted fails", "deny", them.UserResult{Status: "passed", Connected: true}, "failed"},
		{"deny + other error fails", "deny", them.UserResult{Status: "failed", HTTPStatus: 500, Error: "boom"}, "failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := c.in
			applyExpect(c.expect, &r)
			if r.Status != c.want {
				t.Errorf("status = %q, want %q", r.Status, c.want)
			}
		})
	}
}
