package cli

import "testing"

// evalCLI parses input, matches a rule, and evaluates its local condition.
// Returns (matched, allowed, reason).
func evalCLI(t *testing.T, input string) (matched, allowed bool, reason string) {
	t.Helper()
	cmd, err := ParseCLICommand(input)
	if err != nil {
		t.Fatalf("failed to parse %q: %v", input, err)
	}
	rule, found := MatchCLIRule(cmd)
	if !found {
		return false, false, ""
	}
	allowed, reason = EvaluateRule(rule, cmd)
	return true, allowed, reason
}

func TestQoSPolicyGroupCLIRules(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantMatched bool
		wantAllowed bool
	}{
		{"show allowed", "qos policy-group show", true, true},
		{"create customer allowed", "qos policy-group create -policy-group cust1 -vserver svm1 -throughput 500MB/s -iops 5000 -is-shared true", true, true},
		{"create only throughput allowed (S6)", "qos policy-group create -policy-group cust1 -vserver svm1 -throughput 500MB/s", true, true},
		{"create missing policy-group denied", "qos policy-group create -vserver svm1", true, false},
		{"create missing vserver denied", "qos policy-group create -policy-group cust1", true, false},
		{"create reserved name denied (N1)", "qos policy-group create -policy-group svm1-qos-policy -vserver svm1", true, false},
		{"create non-matching -qos-policy suffix allowed", "qos policy-group create -policy-group foo-qos-policy -vserver svm1", true, true},
		{"create with min-throughput denied", "qos policy-group create -policy-group cust1 -vserver svm1 -min-throughput 100", true, false},
		{"modify customer allowed", "qos policy-group modify -policy-group cust1 -vserver svm1 -throughput 800MB/s", true, true},
		{"modify missing vserver allowed", "qos policy-group modify -policy-group cust1 -throughput 800MB/s", true, true},
		{"modify reserved denied (N3)", "qos policy-group modify -policy-group svm1-qos-policy -vserver svm1", true, false},
		// Without -vserver the local condition defers to the external validator (see
		// TestValidateQoSPolicyGroupReserved); local eval allows it.
		{"modify reserved without vserver deferred to external validator", "qos policy-group modify -policy-group svm1-qos-policy -throughput 800MB/s", true, true},
		{"modify with min-throughput denied", "qos policy-group modify -policy-group cust1 -vserver svm1 -min-throughput 50", true, false},
		{"delete customer allowed", "qos policy-group delete -policy-group cust1 -vserver svm1", true, true},
		{"delete missing vserver allowed", "qos policy-group delete -policy-group cust1", true, true},
		{"delete reserved denied (N3)", "qos policy-group delete -policy-group svm1-qos-policy -vserver svm1", true, false},
		// Without -vserver the local condition defers to the external validator (see
		// TestValidateQoSPolicyGroupReserved); local eval allows it.
		{"delete reserved without vserver deferred to external validator", "qos policy-group delete -policy-group svm1-qos-policy", true, true},
		{"rename customer allowed", "qos policy-group rename -policy-group cust1 -new-name cust2 -vserver svm1", true, true},
		{"rename missing vserver denied", "qos policy-group rename -policy-group cust1 -new-name cust2", true, false},
		{"rename to reserved denied (N2)", "qos policy-group rename -policy-group cust1 -new-name svm1-qos-policy -vserver svm1", true, false},
		{"rename of reserved denied (N2)", "qos policy-group rename -policy-group svm1-qos-policy -new-name cust2 -vserver svm1", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, allowed, reason := evalCLI(t, tt.input)
			if matched != tt.wantMatched {
				t.Fatalf("matched = %v, want %v", matched, tt.wantMatched)
			}
			if allowed != tt.wantAllowed {
				t.Fatalf("allowed = %v (reason=%q), want %v", allowed, reason, tt.wantAllowed)
			}
			if !allowed && reason == "" {
				t.Error("expected a non-empty denial reason")
			}
		})
	}
}

func TestVserverModifyQoSPolicyDenied(t *testing.T) {
	// Requirement 1 / N4: customers cannot change the vserver-wide QPG assignment.
	matched, allowed, reason := evalCLI(t, "vserver modify -vserver svm1 -qos-policy-group cust1")
	if !matched {
		t.Fatal("expected vserver modify rule to match")
	}
	if allowed {
		t.Fatalf("expected -qos-policy-group on vserver modify to be denied, reason=%q", reason)
	}
}

func TestNestedQoSSVMVolumeModifyDenied(t *testing.T) {
	inputs := []string{
		"settings nested-qos-svm-vol modify -vserver svm1 -volume vol1",
		"qos settings nested-qos-svm-vol -enable false",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			matched, allowed, reason := evalCLI(t, input)
			if !matched {
				t.Fatal("expected nested SVM volume QoS settings rule to match")
			}
			if allowed {
				t.Fatalf("expected nested SVM volume QoS modification to be denied, reason=%q", reason)
			}
			if reason == "" {
				t.Fatal("expected a non-empty denial reason")
			}
		})
	}
}

func TestQtreeCLIRulesPassThrough(t *testing.T) {
	// Qtree operations are intentionally unregistered so they pass through by default.
	inputs := []string{
		"qtree show",
		"qtree create -vserver svm1 -volume vol1 -qtree qt1 -qos-policy-group cust1",
		"qtree modify -vserver svm1 -volume vol1 -qtree qt1 -qos-policy-group cust2",
		"qtree delete -vserver svm1 -volume vol1 -qtree qt1",
		"qtree rename -vserver svm1 -volume vol1 -qtree qt1 -newname qt2",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			matched, allowed, reason := evalCLI(t, input)
			if matched {
				t.Fatalf("expected no matching rule for %q (pass-through), got allowed=%v reason=%q", input, allowed, reason)
			}
		})
	}
}
