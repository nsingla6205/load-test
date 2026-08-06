package cli

import (
	"context"
	"testing"
)

func findRule(pattern string) *CLIRule {
	for i := range GetCLIRules() {
		if GetCLIRules()[i].Pattern == pattern {
			r := GetCLIRules()[i]
			return &r
		}
	}
	return nil
}

func TestMatchCLIRule(t *testing.T) {
	// Tests match CLI rules defined in rules.go which correspond to rule_map.go

	t.Run("volume commands - corresponds to /api/storage/volumes", func(t *testing.T) {
		tests := []struct {
			name      string
			input     string
			wantAllow bool
		}{
			{
				name:      "volume show allowed",
				input:     "volume show -vserver vs1",
				wantAllow: true,
			},
			{
				name:      "volume create allowed",
				input:     "volume create -vserver vs1 -volume vol1 -size 100g -aggregate aggr1",
				wantAllow: true,
			},
			{
				name:      "volume modify allowed",
				input:     "volume modify -vserver vs1 -volume vol1 -size 200g",
				wantAllow: true,
			},
			{
				name:      "volume delete allowed",
				input:     "volume delete -vserver vs1 -volume vol1",
				wantAllow: true,
			},
			{
				name:      "volume destroy allowed",
				input:     "volume destroy -vserver vs1 -volume vol1",
				wantAllow: true,
			},
			{
				name:      "vol destroy allowed",
				input:     "vol destroy -vserver vs1 -volume vol1",
				wantAllow: true,
			},
			{
				name:      "volume size allowed",
				input:     "volume size -vserver vs1 -volume vol1 -new-size 200g",
				wantAllow: true,
			},
			{
				name:      "vol size allowed",
				input:     "vol size -vserver vs1 -volume vol1 -new-size 100g",
				wantAllow: true,
			},
			{
				name:      "volume clone create allowed",
				input:     "volume clone create -vserver vs1 -flexclone clone1 -parent-volume src1",
				wantAllow: true,
			},
			{
				name:      "vol clone create allowed",
				input:     "vol clone create -vserver vs1 -flexclone clone1 -b src1",
				wantAllow: true,
			},
			{
				name:      "volume clone split start allowed",
				input:     "volume clone split start -vserver vs1 -flexclone clone1",
				wantAllow: true,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cmd, err := ParseCLICommand(tt.input)
				if err != nil {
					t.Fatalf("Failed to parse command: %v", err)
				}

				rule, found := MatchCLIRule(cmd)
				if !found && tt.wantAllow {
					t.Error("Expected to find a matching rule")
				}

				if rule.Allow != tt.wantAllow {
					t.Errorf("Allow = %v, want %v", rule.Allow, tt.wantAllow)
				}
			})
		}
	})

	t.Run("volume clone split stop commands - no explicit rule (pass-through)", func(t *testing.T) {
		tests := []struct {
			name  string
			input string
		}{
			{
				name:  "volume clone split stop no rule",
				input: "volume clone split stop -vserver vs1 -flexclone clone1",
			},
			{
				name:  "vol clone split stop no rule",
				input: "vol clone split stop -vserver vs1 -flexclone clone1",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cmd, err := ParseCLICommand(tt.input)
				if err != nil {
					t.Fatalf("Failed to parse command: %v", err)
				}

				_, found := MatchCLIRule(cmd)
				if found {
					t.Fatal("Expected no explicit matching rule for split stop")
				}
			})
		}
	})

	t.Run("volume clone create conditions", func(t *testing.T) {
		t.Run("missing parent argument fails local condition", func(t *testing.T) {
			cmd, err := ParseCLICommand("volume clone create -vserver vs1 -flexclone clone1")
			if err != nil {
				t.Fatalf("Failed to parse command: %v", err)
			}

			rule, found := MatchCLIRule(cmd)
			if !found {
				t.Fatal("Expected to find clone create rule")
			}

			allowed, reason := EvaluateRule(rule, cmd)
			if allowed {
				t.Fatal("Expected command to be denied")
			}
			if reason == "" {
				t.Fatal("Expected non-empty deny reason")
			}
		})

		t.Run("invalid space-guarantee fails local condition", func(t *testing.T) {
			cmd, err := ParseCLICommand("volume clone create -vserver vs1 -flexclone clone1 -parent-volume src1 -space-guarantee volume")
			if err != nil {
				t.Fatalf("Failed to parse command: %v", err)
			}

			rule, found := MatchCLIRule(cmd)
			if !found {
				t.Fatal("Expected to find clone create rule")
			}

			allowed, reason := EvaluateRule(rule, cmd)
			if allowed {
				t.Fatal("Expected command to be denied")
			}
			if reason == "" {
				t.Fatal("Expected non-empty deny reason")
			}
		})
	})

	t.Run("volume show-footprint commands - allowed with tiering keep-list", func(t *testing.T) {
		tests := []struct {
			name  string
			input string
		}{
			{
				name:  "volume show-footprint allowed",
				input: "volume show-footprint -vserver vs1 -volume vol1",
			},
			{
				name:  "vol show-footprint allowed",
				input: "vol show-footprint -vserver vs1",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cmd, err := ParseCLICommand(tt.input)
				if err != nil {
					t.Fatalf("Failed to parse command: %v", err)
				}

				rule, found := MatchCLIRule(cmd)
				if !found {
					t.Error("Expected to find a matching rule")
				}

				if !rule.Allow {
					t.Error("Expected show-footprint to be allowed")
				}

				allowed, _ := EvaluateRule(rule, cmd)
				if !allowed {
					t.Error("Expected command to be allowed")
				}

				if len(rule.KeepFields) == 0 {
					t.Error("Expected show-footprint to have a KeepFields allow-list")
				}
			})
		}
	})

	t.Run("volume autosize commands - all blocked", func(t *testing.T) {
		tests := []struct {
			name      string
			input     string
			wantAllow bool
			wantFound bool
		}{
			{
				name:      "volume autosize show blocked",
				input:     "volume autosize show -vserver vs1",
				wantAllow: false,
				wantFound: true,
			},
			{
				name:      "vol autosize show blocked",
				input:     "vol autosize show -vserver vs1",
				wantAllow: false,
				wantFound: true,
			},
			{
				name:      "volume autosize modify blocked",
				input:     "volume autosize modify -vserver vs1 -volume vol1 -mode grow -maximum-size 500g",
				wantAllow: false,
				wantFound: true,
			},
			{
				name:      "vol autosize modify blocked",
				input:     "vol autosize modify -vserver vs1 -volume vol1 -mode grow",
				wantAllow: false,
				wantFound: true,
			},
			{
				name:      "volume autosize bare form blocked",
				input:     "volume autosize -vserver vs1 -volume vol1 -maximum-size 500g",
				wantAllow: false,
				wantFound: true,
			},
			{
				name:      "vol autosize bare form blocked",
				input:     "vol autosize -vserver vs1 -volume vol1 -mode off",
				wantAllow: false,
				wantFound: true,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cmd, err := ParseCLICommand(tt.input)
				if err != nil {
					t.Fatalf("Failed to parse command: %v", err)
				}

				rule, found := MatchCLIRule(cmd)
				if found != tt.wantFound {
					t.Errorf("found = %v, want %v", found, tt.wantFound)
				}
				if found && rule.Allow != tt.wantAllow {
					t.Errorf("Allow = %v, want %v", rule.Allow, tt.wantAllow)
				}
			})
		}
	})

	t.Run("flexcache commands - corresponds to /api/storage/flexcache/flexcaches", func(t *testing.T) {
		tests := []struct {
			name      string
			input     string
			wantAllow bool
		}{
			{
				name:      "volume flexcache show allowed",
				input:     "volume flexcache show -vserver vs1",
				wantAllow: true,
			},
			{
				name:      "vol flexcache show allowed",
				input:     "vol flexcache show",
				wantAllow: true,
			},
			{
				name:      "flexcache show allowed",
				input:     "flexcache show -vserver vs1",
				wantAllow: true,
			},
			{
				name:      "volume flexcache create allowed",
				input:     "volume flexcache create -vserver vs1 -volume fc1 -origin-volume orig1 -size 400m",
				wantAllow: true,
			},
			{
				name:      "vol flexcache create allowed",
				input:     "vol flexcache create -vserver vs1 -volume fc1 -origin-volume orig1 -size 400m",
				wantAllow: true,
			},
			{
				name:      "flexcache create allowed",
				input:     "flexcache create -vserver vs1 -volume fc1 -origin-volume orig1 -size 400m",
				wantAllow: true,
			},
			{
				name:      "volume flexcache delete allowed",
				input:     "volume flexcache delete -vserver vs1 -volume fc1",
				wantAllow: true,
			},
			{
				name:      "vol flexcache delete allowed",
				input:     "vol flexcache delete -vserver vs1 -volume fc1",
				wantAllow: true,
			},
			{
				name:      "flexcache delete allowed",
				input:     "flexcache delete -vserver vs1 -volume fc1",
				wantAllow: true,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cmd, err := ParseCLICommand(tt.input)
				if err != nil {
					t.Fatalf("Failed to parse command: %v", err)
				}

				rule, found := MatchCLIRule(cmd)
				if !found && tt.wantAllow {
					t.Error("Expected to find a matching rule")
				}

				if rule.Allow != tt.wantAllow {
					t.Errorf("Allow = %v, want %v", rule.Allow, tt.wantAllow)
				}
			})
		}
	})

	t.Run("security certificate commands - corresponds to /api/security/certificates", func(t *testing.T) {
		tests := []struct {
			input     string
			wantAllow bool
		}{
			{"security certificate show", true},
			{"security certificate create -vserver vs1 -common-name mycert -type server", true},
			{"sec certificate create -vserver vs1 -type client", true},
			{"security certificate install -vserver vs1 -type server", true},
			{"security certificate delete -vserver vs1 -common-name cert1", false},
		}

		for _, tt := range tests {
			t.Run(tt.input, func(t *testing.T) {
				cmd, err := ParseCLICommand(tt.input)
				if err != nil {
					t.Fatalf("Failed to parse command: %v", err)
				}

				rule, _ := MatchCLIRule(cmd)

				if rule.Allow != tt.wantAllow {
					t.Errorf("Allow = %v, want %v", rule.Allow, tt.wantAllow)
				}
			})
		}
	})

	t.Run("vserver object-store-server bucket create NAS-only", func(t *testing.T) {
		valid := "vserver object-store-server bucket create -vserver svm1 -bucket b1 -type nas -nas-path /vol1/share"
		cmd, err := ParseCLICommand(valid)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		rule, found := MatchCLIRule(cmd)
		if !found || rule == nil {
			t.Fatal("expected matching rule for bucket create")
		}
		ok, reason := EvaluateRule(rule, cmd)
		if !ok {
			t.Fatalf("expected allowed: %s", reason)
		}

		badType := "vserver object-store-server bucket create -vserver svm1 -bucket b1 -type s3 -nas-path /"
		cmd2, _ := ParseCLICommand(badType)
		rule2, _ := MatchCLIRule(cmd2)
		ok2, _ := EvaluateRule(rule2, cmd2)
		if ok2 {
			t.Fatal("expected deny when -type s3")
		}

		noNasPath := "vserver object-store-server bucket create -vserver svm1 -bucket b1 -type nas"
		cmd3, _ := ParseCLICommand(noNasPath)
		rule3, _ := MatchCLIRule(cmd3)
		ok3, _ := EvaluateRule(rule3, cmd3)
		if ok3 {
			t.Fatal("expected deny when -nas-path omitted")
		}
	})

	t.Run("standalone set commands - all denied", func(t *testing.T) {
		expectedReason := "Privilege escalation not allowed; use the chained command form (e.g. 'set diag; <command>')"
		tests := []struct {
			name  string
			input string
		}{
			{"set diag", "set diag"},
			{"set diag with args", "set diag -confirm"},
			{"set diagnostic", "set diagnostic"},
			{"set advanced", "set advanced"},
			{"set -privilege diagnostic", "set -privilege diagnostic"},
			{"set -privilege diag", "set -privilege diag"},
			{"set -privilege advanced", "set -privilege advanced"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cmd, err := ParseCLICommand(tt.input)
				if err != nil {
					t.Fatalf("Failed to parse command: %v", err)
				}

				rule, found := MatchCLIRule(cmd)
				if !found {
					t.Fatalf("Expected to find a matching rule for %q", tt.input)
				}
				if rule.Allow {
					t.Errorf("%q should be denied", tt.input)
				}
				if rule.Reason != expectedReason {
					t.Errorf("Reason = %q, want %q", rule.Reason, expectedReason)
				}
			})
		}
	})

	t.Run("nil command", func(t *testing.T) {
		rule, found := MatchCLIRule(nil)

		if found {
			t.Error("Expected not to find a rule for nil command")
		}
		if rule != nil {
			t.Error("Expected nil rule for nil command")
		}
	})

	t.Run("unknown command - no matching rule", func(t *testing.T) {
		cmd := &CLICommand{
			Command:     "unknown",
			Subcommand:  "command",
			FullCommand: "unknown command",
		}

		rule, found := MatchCLIRule(cmd)

		if found {
			t.Error("Expected not to find a rule for unknown command")
		}
		if rule != nil {
			t.Error("Expected nil rule for unknown command")
		}
	})
}

func TestEvaluateRule(t *testing.T) {
	t.Run("allowed rule", func(t *testing.T) {
		rule := &CLIRule{
			Pattern: "volume show",
			Allow:   true,
		}
		cmd := &CLICommand{FullCommand: "volume show"}

		allowed, reason := EvaluateRule(rule, cmd)

		if !allowed {
			t.Errorf("Expected allowed, got denied: %s", reason)
		}
		if reason != "" {
			t.Errorf("Expected empty reason, got %q", reason)
		}
	})

	t.Run("denied rule", func(t *testing.T) {
		rule := &CLIRule{
			Pattern: "system *",
			Allow:   false,
			Reason:  "System commands not allowed",
		}
		cmd := &CLICommand{FullCommand: "system node show"}

		allowed, reason := EvaluateRule(rule, cmd)

		if allowed {
			t.Error("Expected denied, got allowed")
		}
		if reason != "System commands not allowed" {
			t.Errorf("Reason = %q, want %q", reason, "System commands not allowed")
		}
	})

	t.Run("set wildcard denied rule returns reason", func(t *testing.T) {
		rules := GetCLIRules()
		var rule *CLIRule
		for i := range rules {
			if rules[i].Pattern == "set *" {
				rule = &rules[i]
				break
			}
		}
		if rule == nil {
			t.Fatal("set * rule not found")
		}
		cmd, err := ParseCLICommand("set diag")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}

		allowed, reason := EvaluateRule(rule, cmd)

		expectedReason := "Privilege escalation not allowed; use the chained command form (e.g. 'set diag; <command>')"
		if allowed {
			t.Error("set diag should be denied")
		}
		if reason != expectedReason {
			t.Errorf("Reason = %q, want %q", reason, expectedReason)
		}
	})

	t.Run("rule with passing condition", func(t *testing.T) {
		rule := &CLIRule{
			Pattern: "volume create",
			Allow:   true,
			Condition: func(cmd *CLICommand) (bool, string) {
				return true, ""
			},
		}
		cmd := &CLICommand{FullCommand: "volume create"}

		allowed, reason := EvaluateRule(rule, cmd)

		if !allowed {
			t.Errorf("Expected allowed, got denied: %s", reason)
		}
	})

	t.Run("rule with failing condition", func(t *testing.T) {
		rule := &CLIRule{
			Pattern: "volume create",
			Allow:   true,
			Condition: func(cmd *CLICommand) (bool, string) {
				return false, "Missing required argument"
			},
		}
		cmd := &CLICommand{FullCommand: "volume create"}

		allowed, reason := EvaluateRule(rule, cmd)

		if allowed {
			t.Error("Expected denied, got allowed")
		}
		if reason != "Missing required argument" {
			t.Errorf("Reason = %q, want %q", reason, "Missing required argument")
		}
	})

	t.Run("nil rule", func(t *testing.T) {
		allowed, reason := EvaluateRule(nil, &CLICommand{})

		if allowed {
			t.Error("Expected denied for nil rule")
		}
		if reason != "No rule provided" {
			t.Errorf("Reason = %q, want %q", reason, "No rule provided")
		}
	})
}

func TestCLIRuleConditions(t *testing.T) {
	// Test rule with DSL-like conditions (similar to rules_v2/rule_map.go)
	volumeCreateRule := &CLIRule{
		Pattern: "volume create",
		Allow:   true,
		Condition: CLIAnd(
			CLIHasArgs("-vserver", "-volume"),
			CLIIfPresentThenValue("-space-guarantee", "none", "volume"),
		),
	}

	t.Run("valid create command", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-vserver":   "vs1",
				"-volume":    "vol1",
				"-size":      "100g",
				"-aggregate": "aggr1",
			},
		}

		allowed, reason := EvaluateRule(volumeCreateRule, cmd)

		if !allowed {
			t.Errorf("Expected allowed, got denied: %s", reason)
		}
	})

	t.Run("missing vserver", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-volume": "vol1",
			},
		}

		allowed, reason := EvaluateRule(volumeCreateRule, cmd)

		if allowed {
			t.Error("Expected denied for missing vserver")
		}
		if reason != "Missing required argument: -vserver" {
			t.Errorf("Reason = %q, want %q", reason, "Missing required argument: -vserver")
		}
	})

	t.Run("missing volume", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-vserver": "vs1",
			},
		}

		allowed, reason := EvaluateRule(volumeCreateRule, cmd)

		if allowed {
			t.Error("Expected denied for missing volume")
		}
		if reason != "Missing required argument: -volume" {
			t.Errorf("Reason = %q, want %q", reason, "Missing required argument: -volume")
		}
	})

	t.Run("valid space guarantee", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-vserver":         "vs1",
				"-volume":          "vol1",
				"-space-guarantee": "none",
			},
		}

		allowed, reason := EvaluateRule(volumeCreateRule, cmd)

		if !allowed {
			t.Errorf("Expected allowed, got denied: %s", reason)
		}
	})

	t.Run("invalid space guarantee", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-vserver":         "vs1",
				"-volume":          "vol1",
				"-space-guarantee": "invalid",
			},
		}

		allowed, _ := EvaluateRule(volumeCreateRule, cmd)

		if allowed {
			t.Error("Expected denied for invalid space guarantee")
		}
	})

	t.Run("CLIIfPresentThenValue passes when arg not present", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
				// No -space-guarantee - should still pass
			},
		}

		allowed, reason := EvaluateRule(volumeCreateRule, cmd)

		if !allowed {
			t.Errorf("Expected allowed when optional args not present, got denied: %s", reason)
		}
	})

	t.Run("snaplock-type compliance allowed by rule condition", func(t *testing.T) {
		// Volume CLI rules no longer validate -snaplock-type; previously-denied values
		// (e.g. compliance) must not be rejected by the local rule condition.
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-vserver":       "vs1",
				"-volume":        "vol1",
				"-snaplock-type": "compliance",
			},
		}

		allowed, reason := EvaluateRule(volumeCreateRule, cmd)

		if !allowed {
			t.Errorf("Expected allowed with -snaplock-type compliance (rule condition does not validate snaplock-type), got denied: %s", reason)
		}
	})
}

func TestVolumeAutosizeRule_Blocked(t *testing.T) {
	rule := findRule("volume autosize")
	if rule == nil {
		t.Fatal("volume autosize rule not found")
	}
	if rule.Allow {
		t.Fatal("volume autosize rule should be blocked (Allow=false)")
	}

	t.Run("WhenShowCommand_ShouldStillMatchAutosizeRule", func(t *testing.T) {
		cmd, err := ParseCLICommand("volume autosize show -vserver vs1")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchCLIRule(cmd)
		if !found {
			t.Fatal("Expected to find a matching rule")
		}
		if rule.Pattern != "volume autosize" {
			t.Errorf("Expected 'volume autosize' rule, got %q", rule.Pattern)
		}
		if rule.Allow {
			t.Error("Autosize rule should deny (Allow=false)")
		}
	})

	t.Run("WhenModifyCommand_ShouldMatchAndDeny", func(t *testing.T) {
		cmd, err := ParseCLICommand("volume autosize modify -vserver vs1 -volume vol1 -mode grow")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchCLIRule(cmd)
		if !found {
			t.Fatal("Expected to find a matching rule")
		}
		if rule.Allow {
			t.Error("Autosize rule should deny (Allow=false)")
		}
	})
}

// TestVolumeCreateRule_AutosizeBlocked verifies that the volume/vol create rules deny any
// attempt to enable autosize (via -autosize-mode grow/grow_shrink, autosize threshold/limit
// args, or -autosize-reset) while still allowing creates that omit autosize args or explicitly
// set -autosize-mode off. Only the local rule Condition is exercised (EvaluateRule does not run
// the ExternalValidator).
func TestVolumeCreateRule_AutosizeBlocked(t *testing.T) {
	for _, pattern := range []string{"volume create", "vol create"} {
		pattern := pattern
		t.Run(pattern, func(t *testing.T) {
			rule := findRule(pattern)
			if rule == nil {
				t.Fatalf("%s rule not found", pattern)
			}

			// Base command satisfies the rule's required args (-vserver, -volume, -size).
			base := pattern + " -vserver vs1 -volume vol1 -size 100g"

			denyCases := []struct {
				name       string
				input      string
				wantReason string // substring the deny reason must mention
			}{
				{"WhenAutosizeModeGrow_ShouldDeny", base + " -autosize-mode grow", "-autosize-mode"},
				{"WhenAutosizeModeGrowShrink_ShouldDeny", base + " -autosize-mode grow_shrink", "-autosize-mode"},
				{"WhenMaxAutosize_ShouldDeny", base + " -max-autosize 500g", "-max-autosize"},
				{"WhenMinAutosize_ShouldDeny", base + " -min-autosize 10g", "-min-autosize"},
				{"WhenGrowThreshold_ShouldDeny", base + " -autosize-grow-threshold-percent 90", "-autosize-grow-threshold-percent"},
				{"WhenShrinkThreshold_ShouldDeny", base + " -autosize-shrink-threshold-percent 50", "-autosize-shrink-threshold-percent"},
				// -autosize-reset is a boolean flag; the deny reason should call it a "Flag", not an "Argument".
				{"WhenAutosizeResetFlag_ShouldDeny", base + " -autosize-reset", "Flag -autosize-reset is not allowed"},
			}
			for _, tc := range denyCases {
				t.Run(tc.name, func(t *testing.T) {
					cmd, err := ParseCLICommand(tc.input)
					if err != nil {
						t.Fatalf("ParseCLICommand(%q): %v", tc.input, err)
					}
					allowed, reason := EvaluateRule(rule, cmd)
					if allowed {
						t.Errorf("expected %q to be denied", tc.input)
					}
					if !containsSubstring(reason, tc.wantReason) {
						t.Errorf("reason = %q, want to mention %q", reason, tc.wantReason)
					}
				})
			}

			allowCases := []struct {
				name  string
				input string
			}{
				{"WhenNoAutosizeArgs_ShouldAllow", base},
				{"WhenAutosizeModeOff_ShouldAllow", base + " -autosize-mode off"},
			}
			for _, tc := range allowCases {
				t.Run(tc.name, func(t *testing.T) {
					cmd, err := ParseCLICommand(tc.input)
					if err != nil {
						t.Fatalf("ParseCLICommand(%q): %v", tc.input, err)
					}
					allowed, reason := EvaluateRule(rule, cmd)
					if !allowed {
						t.Errorf("expected %q to be allowed, got denied: %s", tc.input, reason)
					}
				})
			}
		})
	}
}

// TestVolumeModifyRule_AutosizeBlocked verifies that the volume/vol modify rules deny any
// attempt to enable autosize (via -autosize-mode grow/grow_shrink, autosize threshold/limit
// args, or -autosize-reset) while still allowing modifies that omit autosize args or explicitly
// set -autosize-mode off. Only the local rule Condition is exercised (EvaluateRule does not run
// the ExternalValidator).
func TestVolumeModifyRule_AutosizeBlocked(t *testing.T) {
	for _, pattern := range []string{"volume modify", "vol modify"} {
		pattern := pattern
		t.Run(pattern, func(t *testing.T) {
			rule := findRule(pattern)
			if rule == nil {
				t.Fatalf("%s rule not found", pattern)
			}

			// Base command satisfies the rule's required args (-vserver, -volume).
			base := pattern + " -vserver vs1 -volume vol1"

			denyCases := []struct {
				name       string
				input      string
				wantReason string // substring the deny reason must mention
			}{
				{"WhenAutosizeModeGrow_ShouldDeny", base + " -autosize-mode grow", "-autosize-mode"},
				{"WhenAutosizeModeGrowShrink_ShouldDeny", base + " -autosize-mode grow_shrink", "-autosize-mode"},
				{"WhenMaxAutosize_ShouldDeny", base + " -max-autosize 500g", "-max-autosize"},
				{"WhenMinAutosize_ShouldDeny", base + " -min-autosize 10g", "-min-autosize"},
				{"WhenGrowThreshold_ShouldDeny", base + " -autosize-grow-threshold-percent 90", "-autosize-grow-threshold-percent"},
				{"WhenShrinkThreshold_ShouldDeny", base + " -autosize-shrink-threshold-percent 50", "-autosize-shrink-threshold-percent"},
				// -autosize-reset is a boolean flag; the deny reason should call it a "Flag", not an "Argument".
				{"WhenAutosizeResetFlag_ShouldDeny", base + " -autosize-reset", "Flag -autosize-reset is not allowed"},
			}
			for _, tc := range denyCases {
				t.Run(tc.name, func(t *testing.T) {
					cmd, err := ParseCLICommand(tc.input)
					if err != nil {
						t.Fatalf("ParseCLICommand(%q): %v", tc.input, err)
					}
					allowed, reason := EvaluateRule(rule, cmd)
					if allowed {
						t.Errorf("expected %q to be denied", tc.input)
					}
					if !containsSubstring(reason, tc.wantReason) {
						t.Errorf("reason = %q, want to mention %q", reason, tc.wantReason)
					}
				})
			}

			allowCases := []struct {
				name  string
				input string
			}{
				{"WhenNoAutosizeArgs_ShouldAllow", base + " -size 200g"},
				{"WhenAutosizeModeOff_ShouldAllow", base + " -autosize-mode off"},
			}
			for _, tc := range allowCases {
				t.Run(tc.name, func(t *testing.T) {
					cmd, err := ParseCLICommand(tc.input)
					if err != nil {
						t.Fatalf("ParseCLICommand(%q): %v", tc.input, err)
					}
					allowed, reason := EvaluateRule(rule, cmd)
					if !allowed {
						t.Errorf("expected %q to be allowed, got denied: %s", tc.input, reason)
					}
				})
			}
		})
	}
}

func TestFlexCacheCreateRule_Conditions(t *testing.T) {
	rule := findRule("volume flexcache create")
	if rule == nil {
		t.Fatal("volume flexcache create rule not found")
	}

	t.Run("WhenAllRequiredArgs_ShouldAllow", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-vserver":       "vs1",
				"-volume":        "fc1",
				"-origin-volume": "orig1",
				"-size":          "400m",
			},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if !allowed {
			t.Errorf("Expected allowed, got denied: %s", reason)
		}
	})

	t.Run("WhenMissingSize_ShouldDeny", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-vserver":       "vs1",
				"-volume":        "fc1",
				"-origin-volume": "orig1",
			},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if allowed {
			t.Error("Expected denied for missing -size")
		}
		if reason != "Missing required argument: -size" {
			t.Errorf("Reason = %q", reason)
		}
	})

	t.Run("WhenMissingOriginVolume_ShouldDeny", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "fc1",
				"-size":    "400m",
			},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if allowed {
			t.Error("Expected denied for missing -origin-volume")
		}
		if reason != "Missing required argument: -origin-volume" {
			t.Errorf("Reason = %q", reason)
		}
	})

	t.Run("WhenSpaceGuaranteeNone_ShouldAllow", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-vserver":         "vs1",
				"-volume":          "fc1",
				"-origin-volume":   "orig1",
				"-size":            "400m",
				"-space-guarantee": "none",
			},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if !allowed {
			t.Errorf("Expected allowed, got denied: %s", reason)
		}
	})

	t.Run("WhenSpaceGuaranteeVolume_ShouldDeny", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-vserver":         "vs1",
				"-volume":          "fc1",
				"-origin-volume":   "orig1",
				"-size":            "400m",
				"-space-guarantee": "volume",
			},
		}
		allowed, _ := EvaluateRule(rule, cmd)
		if allowed {
			t.Error("Expected denied for -space-guarantee volume")
		}
	})

	t.Run("WhenRelativeSizeEnabledTrue_ShouldDeny", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-vserver":                  "vs1",
				"-volume":                   "fc1",
				"-origin-volume":            "orig1",
				"-size":                     "400m",
				"-is-relative-size-enabled": "true",
			},
		}
		allowed, _ := EvaluateRule(rule, cmd)
		if allowed {
			t.Error("Expected denied for -is-relative-size-enabled true")
		}
	})

	t.Run("WhenRelativeSizeEnabledFalse_ShouldAllow", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-vserver":                  "vs1",
				"-volume":                   "fc1",
				"-origin-volume":            "orig1",
				"-size":                     "400m",
				"-is-relative-size-enabled": "false",
			},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if !allowed {
			t.Errorf("Expected allowed, got denied: %s", reason)
		}
	})
}

func TestFlexCacheDeleteRule_Conditions(t *testing.T) {
	rule := findRule("volume flexcache delete")
	if rule == nil {
		t.Fatal("volume flexcache delete rule not found")
	}

	t.Run("WhenAllRequiredArgs_ShouldAllow", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "fc1",
			},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if !allowed {
			t.Errorf("Expected allowed, got denied: %s", reason)
		}
	})

	t.Run("WhenMissingVolume_ShouldDeny", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-vserver": "vs1",
			},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if allowed {
			t.Error("Expected denied for missing -volume")
		}
		if reason != "Missing required argument: -volume" {
			t.Errorf("Reason = %q", reason)
		}
	})

	t.Run("WhenMissingVserver_ShouldDeny", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-volume": "fc1",
			},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if allowed {
			t.Error("Expected denied for missing -vserver")
		}
		if reason != "Missing required argument: -vserver" {
			t.Errorf("Reason = %q", reason)
		}
	})
}

func TestVolumeSizeRule_RequiresVserverVolumeNewSize(t *testing.T) {
	// volume size rule requires -vserver, -volume, -new-size
	input := "volume size -vserver vs1 -volume vol1 -new-size 200g"
	cmd, err := ParseCLICommand(input)
	if err != nil {
		t.Fatalf("ParseCLICommand: %v", err)
	}
	rule, found := MatchCLIRule(cmd)
	if !found {
		t.Fatal("Expected to find volume size rule")
	}
	allowed, reason := EvaluateRule(rule, cmd)
	if !allowed {
		t.Errorf("Expected allowed with all required args, got denied: %s", reason)
	}

	// Missing -new-size must fail
	cmdMissingNewSize := &CLICommand{
		FullCommand: "volume size",
		Arguments:   map[string]string{"-vserver": "vs1", "-volume": "vol1"},
	}
	allowed, reason = EvaluateRule(rule, cmdMissingNewSize)
	if allowed {
		t.Error("Expected denied when -new-size missing")
	}
	if reason != "Missing required argument: -new-size" {
		t.Errorf("Reason = %q, want Missing required argument: -new-size", reason)
	}
}

func TestVolumeCreateRule_SnaplockTypeNotValidated(t *testing.T) {
	// Asserts that the real volume create rule (from rules.go) does not reject
	// commands based on -snaplock-type. A previously-denied value like "compliance"
	// or an unknown value must pass the rule condition (EvaluateRule does not run
	// ExternalValidator).
	input := "volume create -vserver vs1 -volume vol1 -size 100g -aggregate aggr1 -snaplock-type compliance"
	cmd, err := ParseCLICommand(input)
	if err != nil {
		t.Fatalf("ParseCLICommand: %v", err)
	}

	rule, found := MatchCLIRule(cmd)
	if !found {
		t.Fatal("Expected to find a matching rule for volume create")
	}

	allowed, reason := EvaluateRule(rule, cmd)
	if !allowed {
		t.Errorf("Rule condition should allow -snaplock-type compliance (snaplock-type is no longer validated); got denied: %s", reason)
	}
}

func TestCLIConditionBuilders(t *testing.T) {
	t.Run("CLIHasArgs single", func(t *testing.T) {
		cond := CLIHasArgs("-vserver")

		// Argument present
		cmd := &CLICommand{Arguments: map[string]string{"-vserver": "vs1"}}
		if ok, _ := cond(cmd); !ok {
			t.Error("Expected condition to pass when argument is present")
		}

		// Argument missing
		cmd = &CLICommand{Arguments: map[string]string{}}
		if ok, _ := cond(cmd); ok {
			t.Error("Expected condition to fail when argument is missing")
		}
	})

	t.Run("CLIHasArgs multiple", func(t *testing.T) {
		cond := CLIHasArgs("-vserver", "-volume")

		// Both present
		cmd := &CLICommand{Arguments: map[string]string{"-vserver": "vs1", "-volume": "vol1"}}
		if ok, _ := cond(cmd); !ok {
			t.Error("Expected condition to pass when all arguments present")
		}

		// One missing
		cmd = &CLICommand{Arguments: map[string]string{"-vserver": "vs1"}}
		if ok, _ := cond(cmd); ok {
			t.Error("Expected condition to fail when one argument missing")
		}
	})

	t.Run("CLIIfPresentThenEquals", func(t *testing.T) {
		cond := CLIIfPresentThenEquals("-force", "true")

		// Argument not present - should pass
		cmd := &CLICommand{Arguments: map[string]string{}}
		if ok, _ := cond(cmd); !ok {
			t.Error("Expected condition to pass when argument is not present")
		}

		// Correct value
		cmd = &CLICommand{Arguments: map[string]string{"-force": "true"}}
		if ok, _ := cond(cmd); !ok {
			t.Error("Expected condition to pass for correct value")
		}

		// Wrong value
		cmd = &CLICommand{Arguments: map[string]string{"-force": "false"}}
		if ok, _ := cond(cmd); ok {
			t.Error("Expected condition to fail for wrong value")
		}
	})

	t.Run("CLIIfPresentThenValue", func(t *testing.T) {
		cond := CLIIfPresentThenValue("-space-guarantee", "none", "volume")

		// Argument not present - should pass
		cmd := &CLICommand{Arguments: map[string]string{}}
		if ok, _ := cond(cmd); !ok {
			t.Error("Expected condition to pass when argument is not present")
		}

		// Argument with valid value
		cmd = &CLICommand{Arguments: map[string]string{"-space-guarantee": "none"}}
		if ok, _ := cond(cmd); !ok {
			t.Error("Expected condition to pass for valid value")
		}

		// Argument with invalid value
		cmd = &CLICommand{Arguments: map[string]string{"-space-guarantee": "invalid"}}
		if ok, _ := cond(cmd); ok {
			t.Error("Expected condition to fail for invalid value")
		}
	})

	t.Run("CLIAnd", func(t *testing.T) {
		cond := CLIAnd(
			CLIHasArgs("-vserver"),
			CLIHasArgs("-volume"),
		)

		// Both present
		cmd := &CLICommand{Arguments: map[string]string{"-vserver": "vs1", "-volume": "vol1"}}
		if ok, _ := cond(cmd); !ok {
			t.Error("Expected condition to pass when both arguments present")
		}

		// One missing
		cmd = &CLICommand{Arguments: map[string]string{"-vserver": "vs1"}}
		if ok, _ := cond(cmd); ok {
			t.Error("Expected condition to fail when one argument missing")
		}
	})

	t.Run("CLIOr", func(t *testing.T) {
		cond := CLIOr(
			CLIHasArgs("-vserver"),
			CLIHasArgs("-svm"),
		)

		// First present
		cmd := &CLICommand{Arguments: map[string]string{"-vserver": "vs1"}}
		if ok, _ := cond(cmd); !ok {
			t.Error("Expected condition to pass when first argument present")
		}

		// Second present
		cmd = &CLICommand{Arguments: map[string]string{"-svm": "vs1"}}
		if ok, _ := cond(cmd); !ok {
			t.Error("Expected condition to pass when second argument present")
		}

		// Neither present
		cmd = &CLICommand{Arguments: map[string]string{}}
		if ok, _ := cond(cmd); ok {
			t.Error("Expected condition to fail when neither argument present")
		}
	})

	t.Run("CLIRejectArgs single", func(t *testing.T) {
		cond := CLIRejectArgs("-space-guarantee")

		// Argument not present - should pass
		cmd := &CLICommand{Arguments: map[string]string{"-vserver": "vs1"}}
		if ok, _ := cond(cmd); !ok {
			t.Error("Expected condition to pass when rejected argument is absent")
		}

		// Argument present - should fail
		cmd = &CLICommand{Arguments: map[string]string{"-space-guarantee": "none"}}
		if ok, reason := cond(cmd); ok {
			t.Error("Expected condition to fail when rejected argument is present")
		} else if reason != "Argument -space-guarantee is not allowed" {
			t.Errorf("Reason = %q", reason)
		}
	})

	t.Run("CLIRejectArgs multiple", func(t *testing.T) {
		cond := CLIRejectArgs("-space-guarantee", "-space-slo")

		// Neither present - should pass
		cmd := &CLICommand{Arguments: map[string]string{"-vserver": "vs1"}}
		if ok, _ := cond(cmd); !ok {
			t.Error("Expected condition to pass when no rejected arguments present")
		}

		// First present - should fail
		cmd = &CLICommand{Arguments: map[string]string{"-space-guarantee": "none"}}
		if ok, _ := cond(cmd); ok {
			t.Error("Expected condition to fail when first rejected argument present")
		}

		// Second present - should fail
		cmd = &CLICommand{Arguments: map[string]string{"-space-slo": "none"}}
		if ok, _ := cond(cmd); ok {
			t.Error("Expected condition to fail when second rejected argument present")
		}
	})

	t.Run("CLIHasFlag", func(t *testing.T) {
		cond := CLIHasFlag("-force")

		// Flag present
		cmd := &CLICommand{Flags: []string{"-force"}}
		if ok, _ := cond(cmd); !ok {
			t.Error("Expected condition to pass when flag is present")
		}

		// Flag missing
		cmd = &CLICommand{Flags: []string{}}
		if ok, _ := cond(cmd); ok {
			t.Error("Expected condition to fail when flag is missing")
		}
	})

	t.Run("CLIRejectArgsOrFlags", func(t *testing.T) {
		cond := CLIRejectArgsOrFlags("-autosize-mode", "-autosize-reset")

		// Neither present - should pass
		cmd := &CLICommand{Arguments: map[string]string{"-vserver": "vs1"}}
		if ok, reason := cond(cmd); !ok {
			t.Errorf("Expected condition to pass when no rejected names present, got reason: %s", reason)
		}

		// Rejected as a named argument - reason should say "Argument"
		cmd = &CLICommand{Arguments: map[string]string{"-autosize-mode": "grow"}}
		if ok, reason := cond(cmd); ok {
			t.Error("Expected condition to fail when rejected argument present")
		} else if reason != "Argument -autosize-mode is not allowed" {
			t.Errorf("Reason = %q, want %q", reason, "Argument -autosize-mode is not allowed")
		}

		// Rejected as a boolean flag - reason should say "Flag", not "Argument"
		cmd = &CLICommand{Flags: []string{"-autosize-reset"}}
		if ok, reason := cond(cmd); ok {
			t.Error("Expected condition to fail when rejected flag present")
		} else if reason != "Flag -autosize-reset is not allowed" {
			t.Errorf("Reason = %q, want %q", reason, "Flag -autosize-reset is not allowed")
		}
	})
}

func TestGetCLIRules(t *testing.T) {
	rules := GetCLIRules()

	if len(rules) == 0 {
		t.Error("Expected at least one rule")
	}

	// Check that volume show rule exists and has RemoveFields
	found := false
	for _, rule := range rules {
		if rule.Pattern == "volume show" {
			found = true
			if len(rule.RemoveFields) == 0 {
				t.Error("Expected volume show rule to have RemoveFields")
			}
			break
		}
	}
	if !found {
		t.Error("Expected to find volume show rule")
	}
}

// TestSecurityCertificateCreateRule_OnlyAllowsServerClient verifies that the
// rule only permits -type server/client (or absent) and denies every CA type
// (root-ca, server-ca, client-ca).
func TestSecurityCertificateCreateRule_OnlyAllowsServerClient(t *testing.T) {
	rule := findRule("security certificate create")
	if rule == nil {
		t.Fatal("security certificate create rule not found")
	}

	t.Run("WhenTypeServer_ShouldAllow", func(t *testing.T) {
		cmd := &CLICommand{
			FullCommand: "security certificate create",
			Arguments: map[string]string{
				"-vserver":     "vs1",
				"-common-name": "mycert",
				"-type":        "server",
			},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if !allowed {
			t.Errorf("Expected allowed for -type server, got denied: %s", reason)
		}
	})

	t.Run("WhenTypeClient_ShouldAllow", func(t *testing.T) {
		cmd := &CLICommand{
			FullCommand: "security certificate create",
			Arguments: map[string]string{
				"-vserver":     "vs1",
				"-common-name": "mycert",
				"-type":        "client",
			},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if !allowed {
			t.Errorf("Expected allowed for -type client, got denied: %s", reason)
		}
	})

	t.Run("WhenTypeRootCA_ShouldDeny", func(t *testing.T) {
		cmd := &CLICommand{
			FullCommand: "security certificate create",
			Arguments: map[string]string{
				"-vserver":     "vs1",
				"-common-name": "rogue-ca",
				"-type":        "root-ca",
			},
		}
		allowed, _ := EvaluateRule(rule, cmd)
		if allowed {
			t.Error("Expected denied for -type root-ca")
		}
	})

	t.Run("WhenTypeServerCA_ShouldDeny", func(t *testing.T) {
		cmd := &CLICommand{
			FullCommand: "security certificate create",
			Arguments: map[string]string{
				"-vserver":     "vs1",
				"-common-name": "my-ca",
				"-type":        "server-ca",
			},
		}
		allowed, _ := EvaluateRule(rule, cmd)
		if allowed {
			t.Error("Expected denied for -type server-ca (only server/client allowed)")
		}
	})

	t.Run("WhenTypeClientCA_ShouldDeny", func(t *testing.T) {
		cmd := &CLICommand{
			FullCommand: "security certificate create",
			Arguments: map[string]string{
				"-vserver":     "vs1",
				"-common-name": "my-ca",
				"-type":        "client-ca",
			},
		}
		allowed, _ := EvaluateRule(rule, cmd)
		if allowed {
			t.Error("Expected denied for -type client-ca (only server/client allowed)")
		}
	})

	t.Run("WhenTypeAbsent_ShouldAllow", func(t *testing.T) {
		cmd := &CLICommand{
			FullCommand: "security certificate create",
			Arguments: map[string]string{
				"-vserver":     "vs1",
				"-common-name": "mycert",
			},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if !allowed {
			t.Errorf("Expected allowed when -type absent (ONTAP defaults to server), got denied: %s", reason)
		}
	})

	t.Run("WhenShorthandSecForm_ShouldHaveSameCondition", func(t *testing.T) {
		secRule := findRule("sec certificate create")
		if secRule == nil {
			t.Fatal("sec certificate create rule not found")
		}
		cmd := &CLICommand{
			FullCommand: "sec certificate create",
			Arguments: map[string]string{
				"-type": "root-ca",
			},
		}
		allowed, _ := EvaluateRule(secRule, cmd)
		if allowed {
			t.Error("Expected sec certificate create to also deny -type root-ca")
		}
	})
}

func TestVolumeModifyRule_RejectsSpaceGuarantee(t *testing.T) {
	rule := findRule("volume modify")
	if rule == nil {
		t.Fatal("volume modify rule not found")
	}

	t.Run("WhenSpaceGuaranteePresent_ShouldDeny", func(t *testing.T) {
		cmd := &CLICommand{
			FullCommand: "volume modify",
			Arguments: map[string]string{
				"-vserver":         "vs1",
				"-volume":          "vol1",
				"-space-guarantee": "none",
			},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if allowed {
			t.Error("Expected denied when -space-guarantee is present (even with value 'none')")
		}
		if reason != "Argument -space-guarantee is not allowed" {
			t.Errorf("Reason = %q", reason)
		}
	})

	t.Run("WhenSpaceGuaranteeAbsent_ShouldAllow", func(t *testing.T) {
		cmd := &CLICommand{
			FullCommand: "volume modify",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
				"-size":    "200g",
			},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if !allowed {
			t.Errorf("Expected allowed without -space-guarantee, got denied: %s", reason)
		}
	})
}

func TestVolumeShowRemoveFields(t *testing.T) {
	// Assert exact RemoveFields for volume show / vol show (physical and efficiency fields).
	// Corresponds to rule_map.go GET /api/storage/volumes and /api/private/cli/volume response filtering.
	expectedFields := []string{
		"Used Size",
		"Used Percentage",
		"Physical Used",
		"Physical Used Percent",
		"Footprint",
		"Total Metadata",
		"Space Guarantee",
		"Space SLO",
		"Storage Efficiency",
		"Deduplication",
		"Compression",
		"Sis Space Saved",
		"Dedupe Space Saved",
		"Dedupe Space Shared",
		"Compression Space Saved",
		"Efficiency",
		"Performance Tier Inactive User Data",
		"Volume Size Used by Snapshot Copies",
		"Over Provisioned Size",
	}

	rules := GetCLIRules()

	t.Run("WhenVolumeShowRule_ShouldHaveExpectedRemoveFields", func(t *testing.T) {
		var volumeShowRule *CLIRule
		for i := range rules {
			if rules[i].Pattern == "volume show" {
				volumeShowRule = &rules[i]
				break
			}
		}
		if volumeShowRule == nil {
			t.Fatal("volume show rule not found")
		}
		if len(volumeShowRule.RemoveFields) != len(expectedFields) {
			t.Errorf("volume show RemoveFields length = %d, want %d", len(volumeShowRule.RemoveFields), len(expectedFields))
		}
		for i, want := range expectedFields {
			if i >= len(volumeShowRule.RemoveFields) {
				break
			}
			if volumeShowRule.RemoveFields[i] != want {
				t.Errorf("volume show RemoveFields[%d] = %q, want %q", i, volumeShowRule.RemoveFields[i], want)
			}
		}
	})

	t.Run("WhenVolShowRule_ShouldHaveSameRemoveFieldsAsVolumeShow", func(t *testing.T) {
		var volShowRule *CLIRule
		for i := range rules {
			if rules[i].Pattern == "vol show" {
				volShowRule = &rules[i]
				break
			}
		}
		if volShowRule == nil {
			t.Fatal("vol show rule not found")
		}
		if len(volShowRule.RemoveFields) != len(expectedFields) {
			t.Errorf("vol show RemoveFields length = %d, want %d", len(volShowRule.RemoveFields), len(expectedFields))
		}
		for i, want := range expectedFields {
			if i >= len(volShowRule.RemoveFields) {
				break
			}
			if volShowRule.RemoveFields[i] != want {
				t.Errorf("vol show RemoveFields[%d] = %q, want %q", i, volShowRule.RemoveFields[i], want)
			}
		}
	})
}

func TestVolumeShowFootprintAllowedWithKeepFields(t *testing.T) {
	rules := GetCLIRules()

	expectedKeepFields := []string{
		"Vserver",
		"Volume Name",
		"Total Footprint",
		"Volume Footprint for bin0",
		"Volume Footprint for bin1",
	}

	for _, pattern := range []string{"volume show-footprint", "vol show-footprint"} {
		pattern := pattern
		t.Run(pattern, func(t *testing.T) {
			var rule *CLIRule
			for i := range rules {
				if rules[i].Pattern == pattern {
					rule = &rules[i]
					break
				}
			}
			if rule == nil {
				t.Fatalf("%s rule not found", pattern)
			}
			if !rule.Allow {
				t.Errorf("%s should be allowed (tiering footprint exposure)", pattern)
			}
			if len(rule.RemoveFields) != 0 {
				t.Errorf("%s should use KeepFields, not RemoveFields; got %d RemoveFields", pattern, len(rule.RemoveFields))
			}
			if len(rule.KeepFields) != len(expectedKeepFields) {
				t.Fatalf("%s KeepFields length = %d, want %d", pattern, len(rule.KeepFields), len(expectedKeepFields))
			}
			for i, want := range expectedKeepFields {
				if rule.KeepFields[i] != want {
					t.Errorf("%s KeepFields[%d] = %q, want %q", pattern, i, rule.KeepFields[i], want)
				}
			}
		})
	}
}

func TestSetWildcardDenied(t *testing.T) {
	rules := GetCLIRules()

	t.Run("WhenSetWildcardRule_ShouldBeDenied", func(t *testing.T) {
		var rule *CLIRule
		for i := range rules {
			if rules[i].Pattern == "set *" {
				rule = &rules[i]
				break
			}
		}
		if rule == nil {
			t.Fatal("set * rule not found")
		}
		if rule.Allow {
			t.Error("set * should be denied")
		}
		expectedReason := "Privilege escalation not allowed; use the chained command form (e.g. 'set diag; <command>')"
		if rule.Reason != expectedReason {
			t.Errorf("Reason = %q, want %q", rule.Reason, expectedReason)
		}
		if len(rule.RemoveFields) != 0 {
			t.Errorf("Denied rule should have no RemoveFields, got %d", len(rule.RemoveFields))
		}
	})
}

func TestMatchDiagRule_AllCommands(t *testing.T) {
	allowed := []struct {
		name  string
		input string
	}{
		{"WhenVolumeShow_ShouldBeInDiagAllowlist", "volume show -vserver vs1"},
		{"WhenVolShow_ShouldBeInDiagAllowlist", "vol show -vserver vs1"},
		{"WhenDebugDmVserverXc_ShouldBeInDiagAllowlist", "debug dm vserver xc"},
		{"WhenDebugLocksPersistenceShow_ShouldBeInDiagAllowlist", "debug locks persistence show"},
		{"WhenDebugLocksReconstructionShow_ShouldBeInDiagAllowlist", "debug locks persistence reconstruction show"},
		{"WhenDebugLocksReconstructionShowVolume_ShouldBeInDiagAllowlist", "debug locks persistence reconstruction show-volume"},
		{"WhenDebugNetworkTcpdump_ShouldBeInDiagAllowlist", "debug network tcpdump"},
		{"WhenDebugSanLun_ShouldBeInDiagAllowlist", "debug san lun"},
		{"WhenVolumeSnapshotShow_ShouldBeInDiagAllowlist", "volume snapshot show -vserver vs1 -volume vol1"},
		{"WhenVolSnapshotShow_ShouldBeInDiagAllowlist", "vol snapshot show -vserver vs1 -volume vol1"},
		{"WhenVserverNfsClient_ShouldBeInDiagAllowlist", "vserver nfs client"},
	}
	for _, tt := range allowed {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := ParseCLICommand(tt.input)
			if err != nil {
				t.Fatalf("ParseCLICommand(%q): %v", tt.input, err)
			}
			_, found := MatchPrivilegedRule(cmd, GetDiagAllowedRules())
			if !found {
				t.Fatalf("%q should be in diag allowlist", tt.input)
			}
		})
	}

	rejected := []struct {
		name  string
		input string
	}{
		{"WhenVolumeCreate_ShouldNotBeInDiagAllowlist", "volume create -vserver vs1 -volume vol1 -size 100g"},
		{"WhenStatisticsShow_ShouldNotBeInDiagAllowlist", "statistics show"},
		{"WhenSecurityCertDelete_ShouldNotBeInDiagAllowlist", "security certificate delete -vserver vs1"},
		{"WhenClusterAppRecord_ShouldNotBeInDiagAllowlist", "cluster application-record"},
		{"WhenEventGenerate_ShouldNotBeInDiagAllowlist", "event generate"},
		{"WhenSnapmirrorCheck_ShouldNotBeInDiagAllowlist", "snapmirror check"},
		{"WhenLunRescan_ShouldNotBeInDiagAllowlist", "lun rescan"},
		{"WhenVolumeFileMoveStart_ShouldNotBeInDiagAllowlist", "volume file move start -vserver vs1"},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := ParseCLICommand(tt.input)
			if err != nil {
				t.Fatalf("ParseCLICommand(%q): %v", tt.input, err)
			}
			_, found := MatchPrivilegedRule(cmd, GetDiagAllowedRules())
			if found {
				t.Errorf("%q should NOT be in diag allowlist", tt.input)
			}
		})
	}
}

func TestMatchAdvancedRule_StatisticsShow(t *testing.T) {
	cmd, err := ParseCLICommand("statistics show")
	if err != nil {
		t.Fatalf("ParseCLICommand: %v", err)
	}
	rule, found := MatchPrivilegedRule(cmd, GetAdvancedAllowedRules())
	if !found {
		t.Fatal("statistics show should be in advanced allowlist")
	}
	if !rule.Allow {
		t.Error("statistics show should be allowed")
	}
}

func TestMatchAdvancedRule_VolumeCheckMetadataNotAllowed(t *testing.T) {
	cmd, err := ParseCLICommand("volume check metadata")
	if err != nil {
		t.Fatalf("ParseCLICommand: %v", err)
	}
	_, found := MatchPrivilegedRule(cmd, GetAdvancedAllowedRules())
	if found {
		t.Error("volume check metadata should NOT be in advanced allowlist")
	}
}

func TestMatchAdvancedRule_VolumeShow(t *testing.T) {
	expectedRemoveFields := []string{
		"Used Size",
		"Used Percentage",
		"Physical Used",
		"Physical Used Percent",
		"Footprint",
		"Total Metadata",
		"Space Guarantee",
		"Space SLO",
		"Storage Efficiency",
		"Deduplication",
		"Compression",
		"Sis Space Saved",
		"Dedupe Space Saved",
		"Dedupe Space Shared",
		"Compression Space Saved",
		"Efficiency",
		"Performance Tier Inactive User Data",
		"Volume Size Used by Snapshot Copies",
		"Over Provisioned Size",
	}

	t.Run("WhenVolumeShow_ShouldBeInAdvancedAllowlist", func(t *testing.T) {
		cmd, err := ParseCLICommand("volume show -vserver vs1")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchPrivilegedRule(cmd, GetAdvancedAllowedRules())
		if !found {
			t.Fatal("volume show should be in advanced allowlist")
		}
		if !rule.Allow {
			t.Error("volume show should be allowed")
		}
	})

	t.Run("WhenVolShow_ShouldBeInAdvancedAllowlist", func(t *testing.T) {
		cmd, err := ParseCLICommand("vol show -vserver vs1")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchPrivilegedRule(cmd, GetAdvancedAllowedRules())
		if !found {
			t.Fatal("vol show should be in advanced allowlist")
		}
		if !rule.Allow {
			t.Error("vol show should be allowed")
		}
	})

	t.Run("WhenVolumeShow_ShouldHaveRemoveFields", func(t *testing.T) {
		cmd, err := ParseCLICommand("volume show")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchPrivilegedRule(cmd, GetAdvancedAllowedRules())
		if !found {
			t.Fatal("volume show should be in advanced allowlist")
		}
		if len(rule.RemoveFields) == 0 {
			t.Error("volume show should have RemoveFields to strip physical and efficiency properties")
		}
	})

	t.Run("WhenVolShow_ShouldHaveRemoveFields", func(t *testing.T) {
		cmd, err := ParseCLICommand("vol show")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchPrivilegedRule(cmd, GetAdvancedAllowedRules())
		if !found {
			t.Fatal("vol show should be in advanced allowlist")
		}
		if len(rule.RemoveFields) == 0 {
			t.Error("vol show should have RemoveFields to strip physical and efficiency properties")
		}
	})

	t.Run("WhenVolumeShow_RemoveFieldsShouldMatchVolumeShowRemoveFields", func(t *testing.T) {
		cmd, err := ParseCLICommand("volume show")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchPrivilegedRule(cmd, GetAdvancedAllowedRules())
		if !found {
			t.Fatal("volume show should be in advanced allowlist")
		}
		if len(rule.RemoveFields) != len(expectedRemoveFields) {
			t.Errorf("volume show RemoveFields length = %d, want %d", len(rule.RemoveFields), len(expectedRemoveFields))
		}
		for i, want := range expectedRemoveFields {
			if i >= len(rule.RemoveFields) {
				break
			}
			if rule.RemoveFields[i] != want {
				t.Errorf("volume show RemoveFields[%d] = %q, want %q", i, rule.RemoveFields[i], want)
			}
		}
	})

	t.Run("WhenVolShow_RemoveFieldsShouldMatchVolumeShowRemoveFields", func(t *testing.T) {
		cmd, err := ParseCLICommand("vol show")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchPrivilegedRule(cmd, GetAdvancedAllowedRules())
		if !found {
			t.Fatal("vol show should be in advanced allowlist")
		}
		if len(rule.RemoveFields) != len(expectedRemoveFields) {
			t.Errorf("vol show RemoveFields length = %d, want %d", len(rule.RemoveFields), len(expectedRemoveFields))
		}
		for i, want := range expectedRemoveFields {
			if i >= len(rule.RemoveFields) {
				break
			}
			if rule.RemoveFields[i] != want {
				t.Errorf("vol show RemoveFields[%d] = %q, want %q", i, rule.RemoveFields[i], want)
			}
		}
	})

	t.Run("WhenVolumeShowAndVolShow_ShouldHaveIdenticalRemoveFields", func(t *testing.T) {
		volCmd, err := ParseCLICommand("volume show")
		if err != nil {
			t.Fatalf("ParseCLICommand(volume show): %v", err)
		}
		shortCmd, err := ParseCLICommand("vol show")
		if err != nil {
			t.Fatalf("ParseCLICommand(vol show): %v", err)
		}

		volRule, found := MatchPrivilegedRule(volCmd, GetAdvancedAllowedRules())
		if !found {
			t.Fatal("volume show should be in advanced allowlist")
		}
		shortRule, found := MatchPrivilegedRule(shortCmd, GetAdvancedAllowedRules())
		if !found {
			t.Fatal("vol show should be in advanced allowlist")
		}

		if len(volRule.RemoveFields) != len(shortRule.RemoveFields) {
			t.Errorf("RemoveFields length mismatch: volume show=%d, vol show=%d", len(volRule.RemoveFields), len(shortRule.RemoveFields))
		}
		for i := range volRule.RemoveFields {
			if i >= len(shortRule.RemoveFields) {
				break
			}
			if volRule.RemoveFields[i] != shortRule.RemoveFields[i] {
				t.Errorf("RemoveFields[%d] mismatch: volume show=%q, vol show=%q", i, volRule.RemoveFields[i], shortRule.RemoveFields[i])
			}
		}
	})

	t.Run("WhenVolumeCreate_ShouldNotBeInAdvancedAllowlist", func(t *testing.T) {
		cmd, err := ParseCLICommand("volume create -vserver vs1 -volume vol1 -size 100g")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		_, found := MatchPrivilegedRule(cmd, GetAdvancedAllowedRules())
		if found {
			t.Error("volume create should NOT be in advanced allowlist")
		}
	})

	t.Run("WhenVolCreate_ShouldNotBeInAdvancedAllowlist", func(t *testing.T) {
		cmd, err := ParseCLICommand("vol create -vserver vs1 -volume vol1 -size 100g")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		_, found := MatchPrivilegedRule(cmd, GetAdvancedAllowedRules())
		if found {
			t.Error("vol create should NOT be in advanced allowlist")
		}
	})

	t.Run("WhenVolumeShowHasNoCondition_ShouldNotRequireArguments", func(t *testing.T) {
		cmd, err := ParseCLICommand("volume show")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchPrivilegedRule(cmd, GetAdvancedAllowedRules())
		if !found {
			t.Fatal("volume show should be in advanced allowlist")
		}
		if rule.Condition != nil {
			t.Error("volume show advanced rule should have no Condition (no argument constraints)")
		}
	})

	t.Run("WhenVolShowHasNoCondition_ShouldNotRequireArguments", func(t *testing.T) {
		cmd, err := ParseCLICommand("vol show")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchPrivilegedRule(cmd, GetAdvancedAllowedRules())
		if !found {
			t.Fatal("vol show should be in advanced allowlist")
		}
		if rule.Condition != nil {
			t.Error("vol show advanced rule should have no Condition (no argument constraints)")
		}
	})
}

func TestMatchDiagRule_VolumeShow(t *testing.T) {
	t.Run("WhenVolumeShow_ShouldBeInDiagAllowlist", func(t *testing.T) {
		cmd, err := ParseCLICommand("volume show -vserver vs1")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchPrivilegedRule(cmd, GetDiagAllowedRules())
		if !found {
			t.Fatal("volume show should be in diag allowlist")
		}
		if !rule.Allow {
			t.Error("volume show should be allowed")
		}
		if len(rule.RemoveFields) == 0 {
			t.Error("volume show should have RemoveFields to strip physical properties")
		}
	})

	t.Run("WhenVolShow_ShouldBeInDiagAllowlist", func(t *testing.T) {
		cmd, err := ParseCLICommand("vol show -vserver vs1")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchPrivilegedRule(cmd, GetDiagAllowedRules())
		if !found {
			t.Fatal("vol show should be in diag allowlist")
		}
		if !rule.Allow {
			t.Error("vol show should be allowed")
		}
		if len(rule.RemoveFields) == 0 {
			t.Error("vol show should have RemoveFields to strip physical properties")
		}
	})

	t.Run("WhenVolumeShow_RemoveFieldsShouldIncludePhysicalProperties", func(t *testing.T) {
		cmd, err := ParseCLICommand("volume show")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchPrivilegedRule(cmd, GetDiagAllowedRules())
		if !found {
			t.Fatal("volume show should be in diag allowlist")
		}
		physicalFields := map[string]bool{
			"Physical Used":         false,
			"Physical Used Percent": false,
		}
		for _, f := range rule.RemoveFields {
			if _, ok := physicalFields[f]; ok {
				physicalFields[f] = true
			}
		}
		for field, found := range physicalFields {
			if !found {
				t.Errorf("RemoveFields should include %q", field)
			}
		}
	})
}

func TestMatchDiagRule_VolumeSnapshotShow(t *testing.T) {
	t.Run("WhenVolumeSnapshotShow_ShouldBeInDiagAllowlist", func(t *testing.T) {
		cmd, err := ParseCLICommand("volume snapshot show -vserver vs1 -volume vol1")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchPrivilegedRule(cmd, GetDiagAllowedRules())
		if !found {
			t.Fatal("volume snapshot show should be in diag allowlist")
		}
		if !rule.Allow {
			t.Error("volume snapshot show should be allowed")
		}
	})

	t.Run("WhenVolSnapshotShow_ShouldBeInDiagAllowlist", func(t *testing.T) {
		cmd, err := ParseCLICommand("vol snapshot show -vserver vs1 -volume vol1")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchPrivilegedRule(cmd, GetDiagAllowedRules())
		if !found {
			t.Fatal("vol snapshot show should be in diag allowlist")
		}
		if !rule.Allow {
			t.Error("vol snapshot show should be allowed")
		}
	})

	t.Run("WhenVolumeSnapshotShow_ShouldHaveNoCondition", func(t *testing.T) {
		cmd, err := ParseCLICommand("volume snapshot show")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchPrivilegedRule(cmd, GetDiagAllowedRules())
		if !found {
			t.Fatal("volume snapshot show should be in diag allowlist")
		}
		if rule.Condition != nil {
			t.Error("volume snapshot show diag rule should have no Condition")
		}
	})

	t.Run("WhenVolSnapshotShow_ShouldHaveNoCondition", func(t *testing.T) {
		cmd, err := ParseCLICommand("vol snapshot show")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchPrivilegedRule(cmd, GetDiagAllowedRules())
		if !found {
			t.Fatal("vol snapshot show should be in diag allowlist")
		}
		if rule.Condition != nil {
			t.Error("vol snapshot show diag rule should have no Condition")
		}
	})

	t.Run("WhenVolumeSnapshotShow_ShouldHaveNoRemoveFields", func(t *testing.T) {
		cmd, err := ParseCLICommand("volume snapshot show")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		rule, found := MatchPrivilegedRule(cmd, GetDiagAllowedRules())
		if !found {
			t.Fatal("volume snapshot show should be in diag allowlist")
		}
		if len(rule.RemoveFields) != 0 {
			t.Errorf("volume snapshot show should have no RemoveFields, got %d", len(rule.RemoveFields))
		}
	})

	t.Run("WhenVolSnapshotShow_ShouldNotBeInAdvancedAllowlist", func(t *testing.T) {
		cmd, err := ParseCLICommand("vol snapshot show -vserver vs1 -volume vol1")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		_, found := MatchPrivilegedRule(cmd, GetAdvancedAllowedRules())
		if found {
			t.Error("vol snapshot show should NOT be in advanced allowlist")
		}
	})
}

func TestEvaluateRule_DefaultReason(t *testing.T) {
	// Test that denied rule with empty reason gets default message
	t.Run("WhenDeniedRuleHasEmptyReason_ShouldReturnDefaultMessage", func(t *testing.T) {
		rule := &CLIRule{
			Pattern: "test command",
			Allow:   false,
			Reason:  "", // Empty reason
		}
		cmd := &CLICommand{FullCommand: "test command"}

		allowed, reason := EvaluateRule(rule, cmd)

		if allowed {
			t.Error("Expected denied, got allowed")
		}
		if reason != "Command not allowed" {
			t.Errorf("Reason = %q, want %q", reason, "Command not allowed")
		}
	})
}

func TestEvaluateExternalValidator(t *testing.T) {
	t.Run("WhenRuleIsNil_ShouldReturnAllowed", func(t *testing.T) {
		allowed, reason := EvaluateExternalValidator(nil, nil, &CLICommand{})

		if !allowed {
			t.Error("Expected allowed for nil rule")
		}
		if reason != "" {
			t.Errorf("Expected empty reason, got %q", reason)
		}
	})

	t.Run("WhenRuleHasNoExternalValidator_ShouldReturnAllowed", func(t *testing.T) {
		rule := &CLIRule{
			Pattern:           "volume show",
			Allow:             true,
			ExternalValidator: nil,
		}
		cmd := &CLICommand{FullCommand: "volume show"}

		allowed, reason := EvaluateExternalValidator(nil, rule, cmd)

		if !allowed {
			t.Error("Expected allowed when no external validator")
		}
		if reason != "" {
			t.Errorf("Expected empty reason, got %q", reason)
		}
	})

	t.Run("WhenExternalValidatorPasses_ShouldReturnAllowed", func(t *testing.T) {
		rule := &CLIRule{
			Pattern: "volume create",
			Allow:   true,
			ExternalValidator: func(ctx context.Context, cmd *CLICommand) (bool, string) {
				return true, ""
			},
		}
		cmd := &CLICommand{FullCommand: "volume create"}

		allowed, reason := EvaluateExternalValidator(nil, rule, cmd)

		if !allowed {
			t.Error("Expected allowed")
		}
		if reason != "" {
			t.Errorf("Expected empty reason, got %q", reason)
		}
	})

	t.Run("WhenExternalValidatorFails_ShouldReturnDenied", func(t *testing.T) {
		rule := &CLIRule{
			Pattern: "volume create",
			Allow:   true,
			ExternalValidator: func(ctx context.Context, cmd *CLICommand) (bool, string) {
				return false, "External validation failed"
			},
		}
		cmd := &CLICommand{FullCommand: "volume create"}

		allowed, reason := EvaluateExternalValidator(nil, rule, cmd)

		if allowed {
			t.Error("Expected denied")
		}
		if reason != "External validation failed" {
			t.Errorf("Reason = %q, want %q", reason, "External validation failed")
		}
	})
}

func TestHasExternalValidator(t *testing.T) {
	t.Run("WhenRuleIsNil_ShouldReturnFalse", func(t *testing.T) {
		if HasExternalValidator(nil) {
			t.Error("Expected false for nil rule")
		}
	})

	t.Run("WhenRuleHasNoValidator_ShouldReturnFalse", func(t *testing.T) {
		rule := &CLIRule{
			Pattern:           "volume show",
			Allow:             true,
			ExternalValidator: nil,
		}

		if HasExternalValidator(rule) {
			t.Error("Expected false for rule without validator")
		}
	})

	t.Run("WhenRuleHasValidator_ShouldReturnTrue", func(t *testing.T) {
		rule := &CLIRule{
			Pattern: "volume create",
			Allow:   true,
			ExternalValidator: func(ctx context.Context, cmd *CLICommand) (bool, string) {
				return true, ""
			},
		}

		if !HasExternalValidator(rule) {
			t.Error("Expected true for rule with validator")
		}
	})
}

func TestHasInjectArguments(t *testing.T) {
	t.Run("WhenRuleIsNil_ShouldReturnFalse", func(t *testing.T) {
		if HasInjectArguments(nil) {
			t.Error("Expected false for nil rule")
		}
	})

	t.Run("WhenRuleHasNoInjectArguments_ShouldReturnFalse", func(t *testing.T) {
		rule := &CLIRule{
			Pattern:         "volume show",
			Allow:           true,
			InjectArguments: nil,
		}

		if HasInjectArguments(rule) {
			t.Error("Expected false for rule without inject arguments")
		}
	})

	t.Run("WhenRuleHasEmptyInjectArguments_ShouldReturnFalse", func(t *testing.T) {
		rule := &CLIRule{
			Pattern:         "volume show",
			Allow:           true,
			InjectArguments: map[string]string{},
		}

		if HasInjectArguments(rule) {
			t.Error("Expected false for rule with empty inject arguments")
		}
	})

	t.Run("WhenRuleHasInjectArguments_ShouldReturnTrue", func(t *testing.T) {
		rule := &CLIRule{
			Pattern: "volume create",
			Allow:   true,
			InjectArguments: map[string]string{
				"-is-space-enforcement-logical": "true",
			},
		}

		if !HasInjectArguments(rule) {
			t.Error("Expected true for rule with inject arguments")
		}
	})
}

func TestApplyInjectArguments(t *testing.T) {
	t.Run("WhenRuleIsNil_ShouldReturnOriginalCommand", func(t *testing.T) {
		cmd := &CLICommand{
			RawInput:  "volume create -vserver vs1",
			Arguments: map[string]string{"-vserver": "vs1"},
		}

		result := ApplyInjectArguments(cmd, nil)

		if result != cmd.RawInput {
			t.Errorf("Result = %q, want %q", result, cmd.RawInput)
		}
	})

	t.Run("WhenRuleHasEmptyInjectArguments_ShouldReturnOriginalCommand", func(t *testing.T) {
		cmd := &CLICommand{
			RawInput:  "volume create -vserver vs1",
			Arguments: map[string]string{"-vserver": "vs1"},
		}
		rule := &CLIRule{
			Pattern:         "volume create",
			Allow:           true,
			InjectArguments: map[string]string{},
		}

		result := ApplyInjectArguments(cmd, rule)

		if result != cmd.RawInput {
			t.Errorf("Result = %q, want %q", result, cmd.RawInput)
		}
	})

	t.Run("WhenArgumentsMissing_ShouldInjectThem", func(t *testing.T) {
		cmd := &CLICommand{
			RawInput:  "volume create -vserver vs1 -volume vol1 -size 100g",
			Arguments: map[string]string{"-vserver": "vs1", "-volume": "vol1", "-size": "100g"},
		}
		rule := &CLIRule{
			Pattern: "volume create",
			Allow:   true,
			InjectArguments: map[string]string{
				"-is-space-enforcement-logical": "true",
				"-is-space-reporting-logical":   "true",
			},
		}

		result := ApplyInjectArguments(cmd, rule)

		if result == cmd.RawInput {
			t.Error("Expected arguments to be injected")
		}
		if !contains(result, "-is-space-enforcement-logical true") {
			t.Errorf("Expected -is-space-enforcement-logical true to be injected, got %q", result)
		}
		if !contains(result, "-is-space-reporting-logical true") {
			t.Errorf("Expected -is-space-reporting-logical true to be injected, got %q", result)
		}
	})

	t.Run("WhenArgumentsAlreadyExist_ShouldNotInjectThem", func(t *testing.T) {
		cmd := &CLICommand{
			RawInput: "volume create -vserver vs1 -is-space-enforcement-logical true",
			Arguments: map[string]string{
				"-vserver":                      "vs1",
				"-is-space-enforcement-logical": "true",
			},
		}
		rule := &CLIRule{
			Pattern: "volume create",
			Allow:   true,
			InjectArguments: map[string]string{
				"-is-space-enforcement-logical": "true",
				"-is-space-reporting-logical":   "true",
			},
		}

		result := ApplyInjectArguments(cmd, rule)

		// Should only inject the missing argument
		if !contains(result, "-is-space-reporting-logical true") {
			t.Errorf("Expected -is-space-reporting-logical true to be injected, got %q", result)
		}
		// Should not duplicate the existing argument
		count := countOccurrences(result, "-is-space-enforcement-logical")
		if count > 1 {
			t.Errorf("Expected -is-space-enforcement-logical to appear once, got %d times", count)
		}
	})

	t.Run("WhenAllArgumentsAlreadyPresent_ShouldReturnUnchanged", func(t *testing.T) {
		cmd := &CLICommand{
			RawInput: "volume create -vserver vs1 -is-space-enforcement-logical true -is-space-reporting-logical true",
			Arguments: map[string]string{
				"-vserver":                      "vs1",
				"-is-space-enforcement-logical": "true",
				"-is-space-reporting-logical":   "true",
			},
		}
		rule := &CLIRule{
			Pattern: "volume create",
			Allow:   true,
			InjectArguments: map[string]string{
				"-is-space-enforcement-logical": "true",
				"-is-space-reporting-logical":   "true",
			},
		}

		result := ApplyInjectArguments(cmd, rule)

		// When all arguments are already present, result should be unchanged
		if result != cmd.RawInput {
			t.Errorf("Result = %q, want %q (no changes)", result, cmd.RawInput)
		}
	})
}

func TestGetInjectedArguments(t *testing.T) {
	t.Run("WhenRuleIsNil_ShouldReturnNil", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{"-vserver": "vs1"},
		}

		result := GetInjectedArguments(cmd, nil)

		if result != nil {
			t.Errorf("Expected nil, got %v", result)
		}
	})

	t.Run("WhenRuleHasEmptyInjectArguments_ShouldReturnNil", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{"-vserver": "vs1"},
		}
		rule := &CLIRule{
			Pattern:         "volume create",
			Allow:           true,
			InjectArguments: map[string]string{},
		}

		result := GetInjectedArguments(cmd, rule)

		if result != nil {
			t.Errorf("Expected nil, got %v", result)
		}
	})

	t.Run("WhenArgumentsWouldBeInjected_ShouldReturnThem", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{"-vserver": "vs1", "-volume": "vol1"},
		}
		rule := &CLIRule{
			Pattern: "volume create",
			Allow:   true,
			InjectArguments: map[string]string{
				"-is-space-enforcement-logical": "true",
				"-is-space-reporting-logical":   "true",
			},
		}

		result := GetInjectedArguments(cmd, rule)

		if len(result) != 2 {
			t.Errorf("Expected 2 injected arguments, got %d", len(result))
		}
		if result["-is-space-enforcement-logical"] != "true" {
			t.Error("Expected -is-space-enforcement-logical to be injected")
		}
		if result["-is-space-reporting-logical"] != "true" {
			t.Error("Expected -is-space-reporting-logical to be injected")
		}
	})

	t.Run("WhenArgumentsAlreadyPresent_ShouldExcludeThem", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-vserver":                      "vs1",
				"-is-space-enforcement-logical": "true", // Already present
			},
		}
		rule := &CLIRule{
			Pattern: "volume create",
			Allow:   true,
			InjectArguments: map[string]string{
				"-is-space-enforcement-logical": "true",
				"-is-space-reporting-logical":   "true",
			},
		}

		result := GetInjectedArguments(cmd, rule)

		if len(result) != 1 {
			t.Errorf("Expected 1 injected argument, got %d", len(result))
		}
		if _, exists := result["-is-space-enforcement-logical"]; exists {
			t.Error("Should not include already present argument")
		}
		if result["-is-space-reporting-logical"] != "true" {
			t.Error("Expected -is-space-reporting-logical to be in result")
		}
	})

	t.Run("WhenAllArgumentsAlreadyPresent_ShouldReturnNil", func(t *testing.T) {
		cmd := &CLICommand{
			Arguments: map[string]string{
				"-vserver":                      "vs1",
				"-is-space-enforcement-logical": "true",
				"-is-space-reporting-logical":   "true",
			},
		}
		rule := &CLIRule{
			Pattern: "volume create",
			Allow:   true,
			InjectArguments: map[string]string{
				"-is-space-enforcement-logical": "true",
				"-is-space-reporting-logical":   "true",
			},
		}

		result := GetInjectedArguments(cmd, rule)

		if result != nil {
			t.Errorf("Expected nil when all arguments present, got %v", result)
		}
	})
}

func TestVserverCreateRule_Blocked(t *testing.T) {
	rule := findRule("vserver create")
	if rule == nil {
		t.Fatal("vserver create rule not found")
	}

	t.Run("WhenVserverCreate_ShouldBeDenied", func(t *testing.T) {
		allowed, reason := EvaluateRule(rule, &CLICommand{
			FullCommand: "vserver create",
			Arguments:   map[string]string{"-vserver": "new-svm"},
		})
		if allowed {
			t.Error("Expected vserver create to be denied")
		}
		if reason != "SVM creation not allowed" {
			t.Errorf("Expected reason 'SVM creation not allowed', got %q", reason)
		}
	})

	t.Run("WhenMatchCLIRule_ShouldMatchVserverCreate", func(t *testing.T) {
		cmd, err := ParseCLICommand("vserver create -vserver new-svm")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		matched, found := MatchCLIRule(cmd)
		if !found {
			t.Fatal("Expected to find a matching rule for vserver create")
		}
		if matched.Allow {
			t.Error("Expected vserver create rule to deny")
		}
	})
}

func TestVserverDeleteRule_Blocked(t *testing.T) {
	rule := findRule("vserver delete")
	if rule == nil {
		t.Fatal("vserver delete rule not found")
	}

	t.Run("WhenVserverDelete_ShouldBeDenied", func(t *testing.T) {
		allowed, reason := EvaluateRule(rule, &CLICommand{
			FullCommand: "vserver delete",
			Arguments:   map[string]string{"-vserver": "existing-svm"},
		})
		if allowed {
			t.Error("Expected vserver delete to be denied")
		}
		if reason != "SVM deletion not allowed" {
			t.Errorf("Expected reason 'SVM deletion not allowed', got %q", reason)
		}
	})

	t.Run("WhenMatchCLIRule_ShouldMatchVserverDelete", func(t *testing.T) {
		cmd, err := ParseCLICommand("vserver delete -vserver existing-svm")
		if err != nil {
			t.Fatalf("ParseCLICommand: %v", err)
		}
		matched, found := MatchCLIRule(cmd)
		if !found {
			t.Fatal("Expected to find a matching rule for vserver delete")
		}
		if matched.Allow {
			t.Error("Expected vserver delete rule to deny")
		}
	})
}

func TestVserverModifyRule_RestrictedFields(t *testing.T) {
	rule := findRule("vserver modify")
	if rule == nil {
		t.Fatal("vserver modify rule not found")
	}

	t.Run("WhenOnlyLanguage_ShouldAllow", func(t *testing.T) {
		cmd := &CLICommand{
			FullCommand: "vserver modify",
			Arguments:   map[string]string{"-vserver": "vs1", "-language": "en.UTF-8"},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if !allowed {
			t.Errorf("Expected vserver modify with -language to be allowed, got reason: %s", reason)
		}
	})

	t.Run("WhenOnlySnapshotPolicy_ShouldAllow", func(t *testing.T) {
		cmd := &CLICommand{
			FullCommand: "vserver modify",
			Arguments:   map[string]string{"-vserver": "vs1", "-snapshot-policy": "default"},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if !allowed {
			t.Errorf("Expected vserver modify with -snapshot-policy to be allowed, got reason: %s", reason)
		}
	})

	t.Run("WhenSecurityStyle_ShouldDeny", func(t *testing.T) {
		cmd := &CLICommand{
			FullCommand: "vserver modify",
			Arguments:   map[string]string{"-vserver": "vs1", "-security-style": "unix"},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if allowed {
			t.Error("Expected vserver modify with -security-style to be denied")
		}
		if !containsSubstring(reason, "-security-style") {
			t.Errorf("Expected reason to mention -security-style, got %q", reason)
		}
	})

	t.Run("WhenExportPolicy_ShouldDeny", func(t *testing.T) {
		cmd := &CLICommand{
			FullCommand: "vserver modify",
			Arguments:   map[string]string{"-vserver": "vs1", "-export-policy": "default"},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if allowed {
			t.Error("Expected vserver modify with -export-policy to be denied")
		}
		if !containsSubstring(reason, "-export-policy") {
			t.Errorf("Expected reason to mention -export-policy, got %q", reason)
		}
	})

	t.Run("WhenOnlyQuotaPolicy_ShouldAllow", func(t *testing.T) {
		cmd := &CLICommand{
			FullCommand: "vserver modify",
			Arguments:   map[string]string{"-vserver": "vs1", "-quota-policy": "default"},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if !allowed {
			t.Errorf("Expected vserver modify with -quota-policy to be allowed, got reason: %s", reason)
		}
	})

	t.Run("WhenAllAllowedArgs_ShouldAllow", func(t *testing.T) {
		cmd := &CLICommand{
			FullCommand: "vserver modify",
			Arguments:   map[string]string{"-vserver": "vs1", "-language": "C.UTF-8", "-snapshot-policy": "custom1", "-quota-policy": "default"},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if !allowed {
			t.Errorf("Expected vserver modify with all allowed args to pass, got reason: %s", reason)
		}
	})

	t.Run("WhenDisallowedArgPresent_ShouldDeny", func(t *testing.T) {
		cmd := &CLICommand{
			FullCommand: "vserver modify",
			Arguments:   map[string]string{"-vserver": "vs1", "-language": "en.UTF-8", "-comment": "test"},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if allowed {
			t.Error("Expected vserver modify with -comment to be denied")
		}
		if !containsSubstring(reason, "-comment") {
			t.Errorf("Expected reason to mention -comment, got %q", reason)
		}
	})

	t.Run("WhenMissingVserver_ShouldDeny", func(t *testing.T) {
		cmd := &CLICommand{
			FullCommand: "vserver modify",
			Arguments:   map[string]string{"-language": "en.UTF-8"},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if allowed {
			t.Error("Expected vserver modify without -vserver to be denied")
		}
		if !containsSubstring(reason, "-vserver") {
			t.Errorf("Expected reason to mention -vserver, got %q", reason)
		}
	})

	t.Run("WhenNameArg_ShouldDeny", func(t *testing.T) {
		cmd := &CLICommand{
			FullCommand: "vserver modify",
			Arguments:   map[string]string{"-vserver": "vs1", "-name": "new-name"},
		}
		allowed, reason := EvaluateRule(rule, cmd)
		if allowed {
			t.Error("Expected vserver modify with -name to be denied")
		}
		if !containsSubstring(reason, "-name") {
			t.Errorf("Expected reason to mention -name, got %q", reason)
		}
	})
}

func TestCLIOnlyAllowArgs(t *testing.T) {
	t.Run("WhenOnlyAllowedArgs_ShouldPass", func(t *testing.T) {
		cond := CLIOnlyAllowArgs("-vserver", "-language")
		cmd := &CLICommand{
			Arguments: map[string]string{"-vserver": "vs1", "-language": "en.UTF-8"},
		}
		ok, reason := cond(cmd)
		if !ok {
			t.Errorf("Expected allowed, got reason: %s", reason)
		}
	})

	t.Run("WhenDisallowedArgPresent_ShouldFail", func(t *testing.T) {
		cond := CLIOnlyAllowArgs("-vserver", "-language")
		cmd := &CLICommand{
			Arguments: map[string]string{"-vserver": "vs1", "-language": "en.UTF-8", "-name": "new"},
		}
		ok, reason := cond(cmd)
		if ok {
			t.Error("Expected denied for -name argument")
		}
		if !containsSubstring(reason, "-name") {
			t.Errorf("Expected reason to mention -name, got %q", reason)
		}
	})

	t.Run("WhenNoArgs_ShouldPass", func(t *testing.T) {
		cond := CLIOnlyAllowArgs("-vserver")
		cmd := &CLICommand{
			Arguments: map[string]string{},
		}
		ok, reason := cond(cmd)
		if !ok {
			t.Errorf("Expected allowed for empty args, got reason: %s", reason)
		}
	})
}

func TestLUNRules_BlockThickProvisioning(t *testing.T) {
	t.Run("lun create allowed and forces space-reserve disabled when omitted", func(t *testing.T) {
		rule := findRule("lun create")
		if rule == nil {
			t.Fatal("lun create rule not found")
		}

		cmd, err := ParseCLICommand("lun create -vserver vs1 -path /vol/v1/lun1 -size 100g")
		if err != nil {
			t.Fatalf("failed to parse command: %v", err)
		}

		allowed, reason := EvaluateRule(rule, cmd)
		if !allowed {
			t.Errorf("expected lun create to be allowed, got denied: %s", reason)
		}

		result := ApplyInjectArguments(cmd, rule)
		if !contains(result, "-space-reserve disabled") {
			t.Errorf("expected -space-reserve disabled to be injected, got %q", result)
		}
		if !contains(result, "-space-allocation enabled") {
			t.Errorf("expected -space-allocation enabled to be injected, got %q", result)
		}
	})

	t.Run("lun create with space-reserve disabled allowed and not duplicated", func(t *testing.T) {
		rule := findRule("lun create")
		if rule == nil {
			t.Fatal("lun create rule not found")
		}

		cmd, err := ParseCLICommand("lun create -vserver vs1 -path /vol/v1/lun1 -size 100g -space-reserve disabled")
		if err != nil {
			t.Fatalf("failed to parse command: %v", err)
		}

		allowed, reason := EvaluateRule(rule, cmd)
		if !allowed {
			t.Errorf("expected lun create with -space-reserve disabled to be allowed, got: %s", reason)
		}

		result := ApplyInjectArguments(cmd, rule)
		if countOccurrences(result, "-space-reserve") != 1 {
			t.Errorf("expected -space-reserve to appear once, got %q", result)
		}
	})

	t.Run("lun create with space-reserve enabled denied", func(t *testing.T) {
		rule := findRule("lun create")
		if rule == nil {
			t.Fatal("lun create rule not found")
		}

		cmd, err := ParseCLICommand("lun create -vserver vs1 -path /vol/v1/lun1 -size 100g -space-reserve enabled")
		if err != nil {
			t.Fatalf("failed to parse command: %v", err)
		}

		allowed, _ := EvaluateRule(rule, cmd)
		if allowed {
			t.Error("expected lun create with -space-reserve enabled (thick) to be denied")
		}
	})

	t.Run("lun modify re-enabling space-reserve denied", func(t *testing.T) {
		rule := findRule("lun modify")
		if rule == nil {
			t.Fatal("lun modify rule not found")
		}

		cmd, err := ParseCLICommand("lun modify -vserver vs1 -path /vol/v1/lun1 -space-reserve enabled")
		if err != nil {
			t.Fatalf("failed to parse command: %v", err)
		}

		allowed, _ := EvaluateRule(rule, cmd)
		if allowed {
			t.Error("expected lun modify re-enabling space reservation to be denied")
		}
	})

	t.Run("lun modify unrelated change allowed", func(t *testing.T) {
		rule := findRule("lun modify")
		if rule == nil {
			t.Fatal("lun modify rule not found")
		}

		cmd, err := ParseCLICommand("lun modify -vserver vs1 -path /vol/v1/lun1 -comment updated")
		if err != nil {
			t.Fatalf("failed to parse command: %v", err)
		}

		allowed, reason := EvaluateRule(rule, cmd)
		if !allowed {
			t.Errorf("expected lun modify without -space-reserve to be allowed, got: %s", reason)
		}
	})
}

// Helper functions for tests
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func countOccurrences(s, substr string) int {
	count := 0
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			count++
		}
	}
	return count
}
