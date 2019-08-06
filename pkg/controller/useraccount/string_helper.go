package useraccount

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func removeString(items []string, value string) []string {
	result := make([]string, 0, len(items)-1)
	for _, item := range items {
		if item == value {
			continue
		}
		result = append(result, item)
	}
	return result
}
