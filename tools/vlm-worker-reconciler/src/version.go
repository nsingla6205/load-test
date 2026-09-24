package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

type version struct {
	major, minor, patch, level int
}

var (
	// strips any non-P suffix (RC1, X50, GA …) — keeps only X.Y.Z or X.Y.ZPn
	stripSuffixRe = regexp.MustCompile(`^(\d+\.\d+\.\d+(?:P\d+)?).*`)
	versionRe     = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:P(\d+))?$`)
	deployNameRe  = regexp.MustCompile(`^vlm-worker-(\d+)-(\d+)-(\d+)(?:p(\d+))?$`)
)

func parseVersion(s string) (version, bool) {
	if m := stripSuffixRe.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return version{}, false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	level := 0
	if m[4] != "" {
		level, _ = strconv.Atoi(m[4])
	}
	return version{major, minor, patch, level}, true
}

func (v version) lineKey() [3]int { return [3]int{v.major, v.minor, v.patch} }

func (v version) String() string {
	if v.level == 0 {
		return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
	}
	return fmt.Sprintf("%d.%d.%dP%d", v.major, v.minor, v.patch, v.level)
}

func lineLessThan(a, b version) bool {
	ak, bk := a.lineKey(), b.lineKey()
	for i := range ak {
		if ak[i] < bk[i] {
			return true
		}
		if ak[i] > bk[i] {
			return false
		}
	}
	return false
}

func deployNameToVersion(name string) (version, bool) {
	m := deployNameRe.FindStringSubmatch(name)
	if m == nil {
		return version{}, false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	level := 0
	if m[4] != "" {
		level, _ = strconv.Atoi(m[4])
	}
	return version{major, minor, patch, level}, true
}

const drainRoleLabel = "vlm.netapp.io/role"

// isDrainRoleDeployment reports Deployments labeled as Temporal D-patch drain workers.
func isDrainRoleDeployment(labels map[string]string) bool {
	return labels[drainRoleLabel] == "drain"
}

// normalizeVersions strips test suffixes, deduplicates, and sorts.
func normalizeVersions(raw []string) []version {
	seen := map[version]struct{}{}
	var result []version
	for _, s := range raw {
		v, ok := parseVersion(s)
		if !ok {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	sort.Slice(result, func(i, j int) bool {
		ki, kj := result[i].lineKey(), result[j].lineKey()
		for x := range ki {
			if ki[x] != kj[x] {
				return ki[x] < kj[x]
			}
		}
		return result[i].level < result[j].level
	})
	return result
}

// shouldKeep returns true if the deployment with depVer should stay active,
// along with the reason.
func shouldKeep(depVer version, activeVersions []version) (bool, string) {
	depLine := depVer.lineKey()

	// Rule 1: Direct match — version is in the active set
	for _, av := range activeVersions {
		if av.lineKey() == depLine && av.level == depVer.level {
			return true, fmt.Sprintf("direct match (%s is active)", depVer)
		}
	}

	// Rule 2: Lower-line active version — pools may be migrating upward,
	// so all higher-line workers must stay up.
	for _, av := range activeVersions {
		if lineLessThan(av, depVer) {
			return true, fmt.Sprintf("lower-line active version %s requires migration support", av)
		}
	}

	// Rule 3: Patch ladder — same line, keep all levels >= min active level.
	minLevel := -1
	for _, av := range activeVersions {
		if av.lineKey() == depLine {
			if minLevel == -1 || av.level < minLevel {
				minLevel = av.level
			}
		}
	}
	if minLevel != -1 && depVer.level >= minLevel {
		return true, fmt.Sprintf("patch ladder: same line, min active level=%d, this level=%d", minLevel, depVer.level)
	}

	return false, ""
}
