package dora

import "cmp"

// largestFirst returns a comparison that orders by value, largest first, with
// items whose value is nil after all the others. Ties, including between two
// nils, are broken by tie.
func largestFirst[T any](value func(T) *float64, tie func(a, b T) int) func(a, b T) int {
	return func(a, b T) int {
		va, vb := value(a), value(b)
		switch {
		case va == nil && vb == nil:
			return tie(a, b)
		case va == nil:
			return 1
		case vb == nil:
			return -1
		}
		return cmp.Or(cmp.Compare(*vb, *va), tie(a, b))
	}
}
