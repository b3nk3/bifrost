package cmd

import (
	"strings"
	"testing"
)

func TestPreferTagged(t *testing.T) {
	relay := bastionChoice{InstanceID: "i-relay"}
	bastion := bastionChoice{InstanceID: "i-bastion", Tagged: true}

	got := preferTagged([]bastionChoice{relay, bastion})
	if len(got) != 1 || got[0].InstanceID != "i-bastion" {
		t.Errorf("preferTagged() = %v, want only i-bastion", got)
	}

	got = preferTagged([]bastionChoice{relay})
	if len(got) != 1 || got[0].InstanceID != "i-relay" {
		t.Errorf("preferTagged() = %v, want i-relay when none tagged", got)
	}
}

func TestSuperuserWarning(t *testing.T) {
	cases := []struct {
		accountID string
		group     string
	}{
		{"904233092296", "infra-aws-database-admins-stg"},
		{"733573666215", "infra-aws-database-admins-prd"},
	}
	for _, c := range cases {
		got := superuserWarning(c.accountID)
		if !strings.Contains(got, c.group+" Google group") {
			t.Errorf("superuserWarning(%q) = %q, want it to name %s", c.accountID, got, c.group)
		}
		if !strings.Contains(got, "/g/"+c.group+"/members") {
			t.Errorf("superuserWarning(%q) = %q, want the %s members link", c.accountID, got, c.group)
		}
	}
	if got := superuserWarning("733573666215"); strings.Contains(got, "also works") {
		t.Errorf("superuserWarning(prd) = %q, want no mention of the old group", got)
	}
	if got := superuserWarning("111111111111"); !strings.Contains(got, "No su- database users are provisioned") || strings.Contains(got, "groups.google.com") {
		t.Errorf("superuserWarning(other) = %q, want the not-provisioned warning and no group link", got)
	}
}
