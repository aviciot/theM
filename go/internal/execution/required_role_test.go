package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/aviciot/them/internal/epconfig"
)

type fakeTenantRoleChecker struct {
	roleID  string // returned by ResolveRole
	names   map[string]string
	nameErr error
}

func (f *fakeTenantRoleChecker) ResolveRole(context.Context, string, map[string]string, map[string]string) (string, error) {
	return f.roleID, nil
}
func (f *fakeTenantRoleChecker) CheckGrant(context.Context, string, string) error { return nil }
func (f *fakeTenantRoleChecker) RoleName(_ context.Context, id string) (string, error) {
	return f.names[id], f.nameErr
}

func TestExternalCallerHasRole(t *testing.T) {
	names := map[string]string{"r-premium": "jwt-premium", "r-partner": "m2m-partner"}
	cases := []struct {
		name string
		rc   RoleChecker
		want bool
	}{
		{"matching role admitted", &fakeTenantRoleChecker{roleID: "r-premium", names: names}, true},
		{"different role denied", &fakeTenantRoleChecker{roleID: "r-partner", names: names}, false},
		{"no mapping denied", &fakeTenantRoleChecker{roleID: "", names: names}, false},
		{"name lookup error denied", &fakeTenantRoleChecker{roleID: "r-premium", names: names, nameErr: errors.New("db")}, false},
		{"no checker denied", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lc := &Lifecycle{logger: testLogger}
			if c.rc != nil {
				lc.roleChecker = c.rc
			}
			cfg := &epconfig.EPConfig{AccessMode: epconfig.AccessModeExternal, RequiredRole: "jwt-premium"}
			if got := lc.externalCallerHasRole(context.Background(), cfg, ExecutionRequest{}); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
