package detect

import "sort"

// Merge combines provider built-in rules with user rules
// (docs/detection.md#user-overrides): a user rule whose Name equals a
// built-in's Name REPLACES that built-in in place (position-independent —
// the replacement keeps the built-in's slot in the pre-sort sequence used
// for stable ordering); other user rules are added. The result is ordered
// by descending Priority, stable so that built-ins precede added user
// rules at equal priority. Neither input slice is mutated.
func Merge(builtin, user []Rule) []Rule {
	userByName := make(map[string]Rule, len(user))
	for _, u := range user {
		userByName[u.Name] = u
	}

	merged := make([]Rule, 0, len(builtin)+len(user))
	replaced := make(map[string]bool, len(user))
	for _, b := range builtin {
		if u, ok := userByName[b.Name]; ok {
			merged = append(merged, u)
			replaced[b.Name] = true
			continue
		}
		merged = append(merged, b)
	}
	for _, u := range user {
		if replaced[u.Name] {
			continue
		}
		merged = append(merged, u)
	}

	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].Priority > merged[j].Priority
	})

	return merged
}
