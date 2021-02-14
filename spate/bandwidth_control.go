package main

import (
	"sync"
	"sync/atomic"
	"time"
)

type BandwidthControlPoint struct {
	sent_bytes int
	timestamp  time.Time
}

func SimpleBandwidthControl(
	control_points chan BandwidthControlPoint,
	sleep_duration *int64,
	finalize *sync.WaitGroup,
	spawner SpateClientSpawner,
) {
	var data []CSVPoint

	// duration in seconds as (Bytes * 8) / (Bits / second) = second
	target_duration := float64(spawner.packet_size*8) / float64(spawner.bandwidth)
	prev_time := time.Now()

	for point := range control_points {
		duration := point.timestamp.Sub(prev_time)
		prev_time = point.timestamp

		if spawner.bandwidth > 0 {
			atomic.StoreInt64(
				sleep_duration,
				int64((target_duration-duration.Seconds())*float64(time.Second)),
			)
		}

		data = append(data, CSVPoint{Mibps: float64(point.sent_bytes) * 8 / 1024 / 1024 / duration.Seconds()})
	}

	WriteCSV(data)
}

func PidBandwidthControl(
	control_points chan BandwidthControlPoint,
	sleep_duration *int64,
	finalize *sync.WaitGroup,
	spawner SpateClientSpawner,
) {
	var data []CSVPoint
	defer finalize.Done()

	target_KiBps := float64(spawner.bandwidth) / 8.0 / 1024.0
	esum := 0.0
	eold := 0.0
	Kp := 8000.0
	Ki := 6.0
	Kd := 8.0
	Ta := 0.01

	ticker := time.NewTicker(time.Duration(time.Millisecond))
	defer ticker.Stop()

	duration := time.Duration(0)
	sent_bytes := 0
	prev_time := time.Now()

runner:
	for {
		select {
		case <-ticker.C:
			// only do bandwidth control if target bps is specified
			if target_KiBps > 0 {
				// PID controller
				KiBps := float64(sent_bytes) / 1024 / duration.Seconds()
				e := KiBps - target_KiBps
				esum += e
				y := (Kp * e) + (Ki * Ta * esum) + (Kd * (e - eold) / Ta)
				eold = e

				atomic.StoreInt64(sleep_duration, int64(y))
			}
		case point, ok := <-control_points:
			if !ok {
				// control points got closed
				break runner
			}

			// the summing should actually be done by the I-part of the PID
			// but I can't figure out a working parameterization with huge I
			duration += point.timestamp.Sub(prev_time)
			sent_bytes += point.sent_bytes
			prev_time = point.timestamp

			data = append(
				data,
				CSVPoint{
					Mibps: float64(sent_bytes) * 8 / 1024 / 1024 / duration.Seconds(),
				},
			)
		}
	}

	WriteCSV(data)
}
