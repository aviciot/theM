package main

import (
	"fmt"
	"testing"
	"time"
)

func TestMessageAge(t *testing.T) {
	recent := fmt.Sprintf("%d-0", time.Now().Add(-90*time.Second).UnixMilli())
	if a := messageAge(recent); a < 89*time.Second || a > 95*time.Second {
		t.Errorf("age of a 90 s old id = %v", a)
	}
	if a := messageAge("1000-0"); a < stuckPendingMaxAge {
		t.Errorf("a 1970 id must read as older than the stuck threshold, got %v", a)
	}
	for _, bad := range []string{"", "garbage", "abc-0", "-"} {
		if a := messageAge(bad); a != 0 {
			t.Errorf("unparseable id %q must never look stuck, age = %v", bad, a)
		}
	}
}
