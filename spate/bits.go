package main

import (
	"github.com/alecthomas/units"
	"strings"
)

var (
	bytesUnitMap = units.MakeUnitMap("iB", "B", 1024)
	metricBytesUnitMap = units.MakeUnitMap("B", "B", 1000)
	bitsUnitMap = units.MakeUnitMap("ib", "b", 1024)
	metricBitsUnitMap = units.MakeUnitMap("b", "b", 1000)
)

func ParseBits(s string) (int64, error) {
	s = strings.TrimSpace(s)
	n, err := units.ParseUnit(s, bytesUnitMap)
	if err == nil {
		n *= 8
	} else {
		n, err = units.ParseUnit(s, metricBytesUnitMap)

		if err == nil {
			n *= 8
		} else {
			n, err = units.ParseUnit(s, bitsUnitMap)

			if err != nil {
				n, err = units.ParseUnit(s, metricBitsUnitMap)
			}
		}
	}

	return n, err
}
