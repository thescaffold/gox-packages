package objectstore

import "fmt"

// ResolveRange turns a Range into (start, length) over an object of size
// bytes, applying the shared semantics every driver must follow: an end past
// the last byte is clamped, a suffix longer than the object means the whole
// object, and anything starting at or past EOF (or reversed/negative) is
// ErrRangeNotSatisfiable. A nil range is the whole object.
func ResolveRange(rng *Range, size int64) (start, length int64, err error) {
	if rng == nil {
		return 0, size, nil
	}
	unsat := fmt.Errorf("%w: size %d", ErrRangeNotSatisfiable, size)
	switch {
	case rng.Suffix > 0:
		if size == 0 {
			return 0, 0, unsat
		}
		n := rng.Suffix
		if n > size {
			n = size
		}
		return size - n, n, nil
	case rng.Start < 0 || rng.Start >= size:
		return 0, 0, unsat
	case rng.OpenEnd:
		return rng.Start, size - rng.Start, nil
	case rng.End < rng.Start:
		return 0, 0, unsat
	}
	end := rng.End
	if end >= size {
		end = size - 1
	}
	return rng.Start, end - rng.Start + 1, nil
}
