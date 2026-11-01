package scenario

import "github.com/aviciot/them-test-runner/them"

// applyExpect rewrites a user's result for scenarios that expect denial.
// Default ("" or "allow") leaves the result untouched. With "deny" the user
// passes only when the entry point rejected them with HTTP 403.
func applyExpect(expect string, r *them.UserResult) {
	if expect != "deny" {
		return
	}
	if r.HTTPStatus == 403 {
		r.Status = "passed"
		r.Error = ""
		return
	}
	r.Status = "failed"
	if r.Error == "" {
		r.Error = "expected denial (403) but the user was admitted"
	}
}
