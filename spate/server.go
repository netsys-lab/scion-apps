package main

import (
	"time"

	"github.com/netsec-ethz/scion-apps/pkg/appnet"
)

type SpateServerSpawner struct {
	runtime_duration time.Duration
	port             uint16
	packet_size      int
}

func NewSpateServerSpawner() SpateServerSpawner {
	var runtime_duration, _ = time.ParseDuration("1s")
	return SpateServerSpawner{
		runtime_duration: runtime_duration,
		port:             1337,
		packet_size:      1208,
	}
}

func (s SpateServerSpawner) Port(port uint16) SpateServerSpawner {
	s.port = port
	return s
}

func (s SpateServerSpawner) RuntimeDuration(runtime_duration time.Duration) SpateServerSpawner {
	s.runtime_duration = runtime_duration
	return s
}

func (s SpateServerSpawner) PacketSize(packet_size int) SpateServerSpawner {
	s.packet_size = packet_size
	return s
}

func (s SpateServerSpawner) Spawn() error {
	Info("Listening for incoming connections on port %d...", s.port)
	conn, err := appnet.ListenPort(s.port)
	if err != nil {
		Error("Listening for incoming connections failed: %s", err)
		return err
	}

	// Wait for first packet
	recvbuf := make([]byte, 2500)
	_, err = conn.Read(recvbuf)
	if err != nil {
		Warn("An error occurred while waiting for incoming packets: %s", err)
		return err
	}
	Info("Connection established, receiving packets from client for %s...", s.runtime_duration)

	start := time.Now()
	end := start.Add(s.runtime_duration)
	bytes_received := 0

	//... handling logic, fetch individual packet
	for {
		// Handle client requests
		resp_length, err := conn.Read(recvbuf)
		if err != nil {
			Warn("An error occurred while receiving remote packets: %s", err)
			continue
		}

		// invalid length
		if resp_length != s.packet_size {
			Warn("Received packet was of wrong size: got %d but expected %d", resp_length, s.packet_size)
			continue
		}

		bytes_received += resp_length

		if time.Until(end) <= 0 {
			break
		}
	}
	conn.Close()
	Info("Finished receiving packets from client!")
	elapsed := time.Since(start)
	throughput := float64(bytes_received) / elapsed.Seconds() / 1024.0 / 1024.0 * 8.0

	Info("Measured throughput: %f Mibit/s\n", throughput)

	return nil
}
