// Copyright 2026 Proxi projekt d.o.o.
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// timeLayout is how dates are shown. ISO until main picks the user's.
var timeLayout = isoLayout

const isoLayout = "2006-01-02 15:04"

// Go has no locale data, so this is the part of it ccs needs: the order and
// separator of a numeric date and the 12 or 24 hour clock, by region. Days,
// months and hours stay zero-padded everywhere so the columns line up.
// Regions not listed get ISO dates, which nobody misreads.
var (
	dateLayouts = map[string][]string{
		"02.01.2006": {"AT", "BA", "BG", "BY", "CH", "CZ", "DE", "DK", "EE", "FI", "GE", "HR", "IL", "IS",
			"KZ", "LI", "LV", "ME", "MK", "NO", "PL", "RO", "RS", "RU", "SI", "SK", "TR", "UA"},
		"02/01/2006": {"AE", "AR", "AU", "BE", "BR", "CO", "CY", "EG", "ES", "FR", "GB", "GR", "HK", "ID",
			"IE", "IN", "IT", "LU", "MT", "MX", "MY", "NZ", "PE", "PK", "PT", "SA", "SG", "TH", "UY", "VN"},
		"02-01-2006": {"CL", "NL"},
		"01/02/2006": {"PH", "US"},
		"2006/01/02": {"CN", "JP", "TW", "ZA"},
		"2006.01.02": {"HU", "KR"},
		"2006-01-02": {"CA", "LT", "SE"},
	}
	twelveHour = []string{"AU", "CA", "CO", "EG", "IN", "NZ", "PH", "PK", "SA", "US"}
)

func layoutFor(region string) string {
	for layout, regions := range dateLayouts {
		for _, r := range regions {
			if r != region {
				continue
			}
			for _, t := range twelveHour {
				if t == region {
					return layout + " 03:04 PM"
				}
			}
			return layout + " 15:04"
		}
	}
	return isoLayout
}

// regionOf pulls the region out of a locale name: "hr_HR.UTF-8" gives HR,
// "sr_Latn_RS" gives RS, and macOS's "en_US@rg=hrzzzz" (English, but Croatian
// formats) gives HR. "C", "POSIX" and bare languages give "".
func regionOf(locale string) string {
	if i := strings.Index(locale, "@rg="); i >= 0 && len(locale) >= i+6 {
		return strings.ToUpper(locale[i+4 : i+6])
	}
	locale, _, _ = strings.Cut(locale, "@")
	locale, _, _ = strings.Cut(locale, ".")
	parts := strings.FieldsFunc(locale, func(r rune) bool { return r == '_' || r == '-' })
	if len(parts) < 2 {
		return ""
	}
	if last := parts[len(parts)-1]; len(last) == 2 {
		return strings.ToUpper(last)
	}
	return ""
}

// userRegion follows the usual order: LC_ALL, then LC_TIME, then LANG. On
// macOS the region from System Settings goes before LANG, because terminals
// there tend to set LANG to en_US.UTF-8 whatever the user's formats are.
func userRegion() string {
	for _, v := range []string{"LC_ALL", "LC_TIME"} {
		if l := os.Getenv(v); l != "" {
			return regionOf(l)
		}
	}
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("defaults", "read", "-g", "AppleLocale").Output(); err == nil {
			if r := regionOf(strings.TrimSpace(string(out))); r != "" {
				return r
			}
		}
	}
	return regionOf(os.Getenv("LANG"))
}
