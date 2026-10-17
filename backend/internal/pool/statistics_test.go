package pool

import (
	"math"
	"testing"
)

func TestUsageTotalsUnknownZeroAndOverflow(t *testing.T) {
	var sum byteTotal
	sum.add(nil)
	if sum.result() != nil || sum.count != 0 {
		t.Fatal("unknown became zero")
	}
	zero := int64(0)
	sum.add(&zero)
	if sum.result() == nil || *sum.result() != 0 {
		t.Fatal("known zero lost")
	}
	max := int64(math.MaxInt64)
	one := int64(1)
	sum.add(&max)
	sum.add(&one)
	sum.add(&one)
	if !sum.overflow || sum.result() != nil || sum.count != 4 {
		t.Fatal("overflow recovered an incorrect subtotal")
	}
}
