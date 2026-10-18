package resolve

// closestName returns the nearest name in candidates, or "" when none is
// close enough to suggest.
func closestName(name string, candidates []string) string {
	best := ""
	bestDist := 3
	for _, c := range candidates {
		if c == name {
			continue
		}
		d := editDistance(name, c)
		if d < bestDist || d == bestDist && best != "" && c < best {
			best = c
			bestDist = d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
