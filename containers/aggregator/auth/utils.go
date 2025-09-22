package auth

func contains(arr []Scope, target Scope) bool {
	for _, val := range arr {
		if val == target {
			return true
		}
	}
	return false
}
