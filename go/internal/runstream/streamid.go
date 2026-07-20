package runstream

import (
	"strconv"
	"strings"
)

// Redis stream IDs have the form "<ms>-<seq>" where both parts are unsigned
// 64-bit integers. These helpers compare and decrement IDs without allocating
// big integers, which is enough for cursor math on well-formed IDs.

// parseStreamID splits "<ms>-<seq>" into its two components. A missing "-<seq>"
// defaults the sequence to 0. Unparseable parts default to 0 so comparisons
// degrade gracefully rather than panicking on malformed input.
func parseStreamID(id string) (ms uint64, seq uint64) {
	dash := strings.IndexByte(id, '-')
	if dash < 0 {
		ms, _ = strconv.ParseUint(id, 10, 64)
		return ms, 0
	}
	ms, _ = strconv.ParseUint(id[:dash], 10, 64)
	seq, _ = strconv.ParseUint(id[dash+1:], 10, 64)
	return ms, seq
}

// compareStreamIDs returns -1 if a < b, 0 if a == b, +1 if a > b.
func compareStreamIDs(a, b string) int {
	am, as := parseStreamID(a)
	bm, bs := parseStreamID(b)
	switch {
	case am < bm:
		return -1
	case am > bm:
		return 1
	case as < bs:
		return -1
	case as > bs:
		return 1
	default:
		return 0
	}
}

// exclusivePredecessor returns a stream ID strictly less than id, chosen so that
// an inclusive XRANGE from the returned ID includes id itself. Decrements the
// sequence part when possible; otherwise decrements the millisecond part and
// sets the sequence to its maximum. Returns "0-0" when id is already the minimum.
func exclusivePredecessor(id string) string {
	ms, seq := parseStreamID(id)
	switch {
	case seq > 0:
		return strconv.FormatUint(ms, 10) + "-" + strconv.FormatUint(seq-1, 10)
	case ms > 0:
		return strconv.FormatUint(ms-1, 10) + "-" + strconv.FormatUint(^uint64(0), 10)
	default:
		return "0-0"
	}
}
