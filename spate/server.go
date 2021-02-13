package main

import (
	"time"
	"net"
	"os"
	"os/signal"

	"github.com/netsec-ethz/scion-apps/pkg/appnet"
	"github.com/fatih/color"
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
	recvbuf := make([]byte, s.packet_size)
	_, err = conn.Read(recvbuf)
	if err != nil {
		Error("An error occurred while waiting for incoming packets: %s", err)
		return err
	}
	Info("Connection established, receiving packets from client for %s...", s.runtime_duration)

	bytes_received := 0
	packets_received := 0
	cancel := make(chan os.Signal, 1)
	signal.Notify(cancel, os.Interrupt)

	start := time.Now()
	end := start.Add(s.runtime_duration)

	//... handling logic, fetch individual packet
	runner: for {
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
		packets_received += 1

		if time.Until(end) <= 0 {
			break runner
		}

		select {
		case <-cancel:
			Info("Received interrupt signal, canceling measurements...")
			break runner
		default:
			// nop
		}
	}

	Info("Measurements finished!")
	elapsed := time.Since(start)
	throughput := float64(bytes_received) / elapsed.Seconds() / 1024.0 / 1024.0 * 8.0

	Info("Notifying clients to stop sending...")
	remote_addrs := make(map[net.Addr]bool)
	timeout_duration, _ := time.ParseDuration("100ms")
	timeout := time.Now().Add(timeout_duration)
	for {
		_, addr, _ := conn.ReadFrom(recvbuf)
		remote_addrs[addr] = true
		if time.Until(timeout) <= 0 {
			break
		}
	}
	for addr := range remote_addrs {
		conn.WriteTo([]byte("stop"), addr)
	}

	heading := color.New(color.Bold, color.Underline).Sprint("Measurement Results")
	deco := color.New(color.Bold).Sprint("=====")
	Info("    %s %s %s", deco, heading, deco)
	Info("     Received data: %v KiB", bytes_received / 1024.0)
	Info("  Received packets: %v packets", packets_received)
	Info("       Packet size: %v B", s.packet_size)
	Info("          Duration: %s", elapsed)
	Info("        Throughput: %v Mibit/s", throughput)

	return nil
}
