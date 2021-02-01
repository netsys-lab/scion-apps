package main

import (
	"fmt"
	"math/rand"

	//"github.com/netsec-ethz/scion-apps/pkg/appnet"
)

// Xorshift
type FastRand uint64
func NewFastRand() FastRand {
	return FastRand(rand.Uint64())
}

func (r FastRand) Get() uint64 {
	r ^= r << 13
	r ^= r >> 7
	r ^= r << 17
	return uint64(r)
}

func spawnServer() {

}

func main() {
	fmt.Println("Caution, the flood! 🌊")

	var frand = NewFastRand()
	num := frand.Get()
	fmt.Printf("%x\n", num)
}
