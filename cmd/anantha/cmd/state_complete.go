package cmd

import "fmt"

// stateLooksComplete returns true when LoadedValues looks like it has
// roughly the data a fully-populated install would carry: node-level
// metadata, plus per-active-zone schedule and activity coverage. Used by
// the index renderer to decide whether to suggest a refresh in the button
// pre-text.
//
// "Active zone" is detected via live state metrics (rt/htsp/clsp), not the
// <N>/enabled flag. Zone 1 has no /enabled field at all on a single-zone
// install, but does have live state. Disabled zones (2-8 on a single-zone
// install) have schedule/activity definitions but no live state, so we skip
// them here.
//
// The heuristic is deliberately permissive: requiring "at least one period
// per day" rather than "all 5" so that a thermostat configured with fewer
// than 5 periods doesn't get falsely flagged as incomplete.
func stateLooksComplete(lv *LoadedValues) bool {
	snap := lv.Snapshot()

	has := func(key string) bool {
		_, ok := snap[key]
		return ok
	}

	// Node-level metrics that always exist on a fully-populated install.
	nodeKeys := []string{
		"system/mode", "system/oat",
		"sensor/wallControl/rt", "sensor/wallControl/rh",
		"profile/model", "profile/firmware", "profile/brand", "profile/serial",
	}
	for _, k := range nodeKeys {
		if !has(k) {
			return false
		}
	}

	days := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	activities := []string{"home", "away", "sleep", "wake", "manual"}

	foundActiveZone := false
	for n := 1; n <= 8; n++ {
		zone := fmt.Sprintf("%d", n)

		// Active-zone signal: live state metrics present.
		if !has(zone+"/rt") || !has(zone+"/htsp") || !has(zone+"/clsp") {
			continue
		}
		foundActiveZone = true

		// Each day must have at least one fully-formed period.
		for _, day := range days {
			anyPeriod := false
			for p := 1; p <= 5; p++ {
				base := fmt.Sprintf("%s/program/%s/period %d", zone, day, p)
				if has(base+"/time") && has(base+"/activity") && has(base+"/enabled") {
					anyPeriod = true
					break
				}
			}
			if !anyPeriod {
				return false
			}
		}

		// At least 4 of 5 activities must have htsp set. The 4-of-5 threshold
		// allows for a user-disabled activity slot.
		actCount := 0
		for _, a := range activities {
			if has(fmt.Sprintf("%s/activities/%s/htsp", zone, a)) {
				actCount++
			}
		}
		if actCount < 4 {
			return false
		}
	}

	// Sanity: at least one zone must be active.
	return foundActiveZone
}
