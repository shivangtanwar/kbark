// SPDX-License-Identifier: Apache-2.0

package doctor

import "fmt"

// checkConfig produces the "config" and "profile" rows.
//
//   - config row: green when a file at ConfigPath loaded successfully;
//     green-but-noted when the file is absent (built-in defaults in
//     use); yellow when UserConfigDir was unavailable entirely.
//   - profile row: red if ProfileErr is set (typo in --profile, or
//     malformed config); green otherwise with "name → provider model".
func checkConfig(opts Options) []Result {
	var out []Result

	switch {
	case opts.ConfigPath != "" && opts.ConfigLoaded:
		out = append(out, Result{Name: "config", Status: Green, Detail: opts.ConfigPath})
	case opts.ConfigPath != "":
		out = append(out, Result{
			Name:   "config",
			Status: Green,
			Detail: "built-in defaults (no " + opts.ConfigPath + ")",
		})
	default:
		out = append(out, Result{
			Name:   "config",
			Status: Yellow,
			Detail: "built-in defaults (UserConfigDir unavailable)",
		})
	}

	if opts.ProfileErr != nil {
		out = append(out, Result{Name: "profile", Status: Red, Detail: opts.ProfileErr.Error()})
		return out
	}
	if opts.Profile == "" {
		out = append(out, Result{Name: "profile", Status: Yellow, Detail: "unresolved"})
		return out
	}
	detail := fmt.Sprintf("%s → %s %s", opts.Profile, opts.Provider, opts.Model)
	out = append(out, Result{Name: "profile", Status: Green, Detail: detail})
	return out
}

// markActive appends "· active" + the model identifier to the detail
// when this result corresponds to the active profile's provider. So
// a user with three configured providers can see at a glance which
// one is being used. Only marks providers that are actually reachable
// (Green) — a RED active provider already shows the failure detail.
func markActive(r Result, opts Options) Result {
	if r.Name != opts.Provider || opts.Provider == "" {
		return r
	}
	if r.Status != Green {
		return r
	}
	r.Detail = r.Detail + " · active (" + opts.Model + ")"
	return r
}
