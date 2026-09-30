package dialrule

import "testing"

func TestRules(t *testing.T) {
	rs, err := Parse("_1XXX\n_0NXXXXXXXXX ; national\n-_0900.\n*43\n")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"1001": true, "1999": true, "999": false, "10011": false,
		"02125550000": true, "01125550000": false, "09001234567": false,
		"*43": true, "*44": false, "0900": false,
	}
	for n, want := range cases {
		if got := rs.Allowed(n); got != want {
			t.Errorf("%s: got %v, want %v", n, got, want)
		}
	}
}

func TestOnlyDeny(t *testing.T) {
	rs, err := Parse("-_00.\n-_+.")
	if err != nil {
		t.Fatal(err)
	}
	if !rs.Allowed("1234") || rs.Allowed("0049123") || rs.Allowed("+49123") {
		t.Fatal("deny-only rules")
	}
}

func TestEmptyAllowsAll(t *testing.T) {
	rs, _ := Parse("  \n; comment only\n")
	if !rs.Allowed("anything") {
		t.Fatal("empty rules must allow")
	}
	var nilRules *Rules
	if !nilRules.Allowed("x") {
		t.Fatal("nil rules must allow")
	}
}

func TestSetsAndWildcards(t *testing.T) {
	rs, err := Parse("_[1-3]0[05]\n_7!")
	if err != nil {
		t.Fatal(err)
	}
	for n, want := range map[string]bool{"100": true, "305": true, "405": false, "101": false, "7": true, "7123": true} {
		if rs.Allowed(n) != want {
			t.Errorf("%s: want %v", n, want)
		}
	}
}

func TestInvalid(t *testing.T) {
	for _, bad := range []string{"_1.2", "_[12", "_[]", "_1a", "12 34", "_[a-z]", "_"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
