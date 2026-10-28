package controllers

import "testing"

func TestClientPackageAuthorization(t *testing.T) {
	for _, c := range []struct {
		auth, admin bool
		owner, id   int
		want        bool
	}{
		{false, false, 0, 1, false}, {false, true, 0, 1, false},
		{true, true, 0, 1, true}, {true, false, 2, 2, true},
		{true, false, 2, 3, false}, {true, false, 0, 3, false},
		{true, true, 0, 0, false}, {true, true, 0, -1, false},
	} {
		if got := canDownloadClient(c.auth, c.admin, c.owner, c.id); got != c.want {
			t.Fatalf("authorization mismatch: %+v", c)
		}
	}
}
