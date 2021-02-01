package main

import (
	"testing"
)

// achieves ~30 GB/s on Ryzen 5 3600
func BenchmarkXorshift(b *testing.B) {
	var frand = NewFastRand()
	for n := 0; n < b.N; n++ {
		// get 1 GibiByte of random data
		for c := 0; c < 134217728; c++ {
			frand.Get()
		}
	}
}
