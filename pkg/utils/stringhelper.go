package utils

import "strings"

// SplitCommaSeparatedList acts exactly the same as strings.Split(s, ",") but returns an empty slice for empty strings.
// To be used when, for example, we want to get an empty slice for empty comma separated list:
// strings.Split("", ",") returns [""] while SplitCommaSeparatedList("") returns []
func SplitCommaSeparatedList(s string) []string {
	if len(s) == 0 {
		return []string{}
	}
	return strings.Split(s, ",")
}
