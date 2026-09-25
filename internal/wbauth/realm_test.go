package wbauth

import "testing"

func TestResolveRealmExplicit(t *testing.T) {
	cases := []struct {
		explicit, domain, want string
	}{
		{"global", "www.codebuddy.cn", "global"},
		{"cn", "www.workbuddy.ai", "cn"},
		{"", "www.workbuddy.ai", "global"},
		{"", "", "cn"},
	}
	for _, c := range cases {
		if got := ResolveRealm(c.explicit, c.domain); got != c.want {
			t.Errorf("ResolveRealm(%q,%q)=%q want %q", c.explicit, c.domain, got, c.want)
		}
	}
}

func TestIsGlobalDomain(t *testing.T) {
	cases := []struct {
		domain string
		want   bool
	}{
		{"www.workbuddy.ai", true},
		{"workbuddy.ai", true},
		{"sub.workbuddy.ai", true},
		{"www.codebuddy.cn", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsGlobalDomain(c.domain); got != c.want {
			t.Errorf("IsGlobalDomain(%q)=%v want %v", c.domain, got, c.want)
		}
	}
}

func TestRealmBase(t *testing.T) {
	if got := RealmBase("global"); got != BaseGlobal {
		t.Errorf("RealmBase(global)=%q", got)
	}
	if got := RealmBase("cn"); got != BaseCN {
		t.Errorf("RealmBase(cn)=%q", got)
	}
	if got := RealmBase(""); got != BaseCN {
		t.Errorf("RealmBase(empty)=%q", got)
	}
}

func TestRealmOrigin(t *testing.T) {
	if got := RealmOrigin("global"); got != OriginGlobal {
		t.Errorf("RealmOrigin(global)=%q", got)
	}
	if got := RealmOrigin("cn"); got != OriginCN {
		t.Errorf("RealmOrigin(cn)=%q", got)
	}
}
