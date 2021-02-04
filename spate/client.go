package main

import (
	"time"

	"github.com/netsec-ethz/scion-apps/pkg/appnet"
)

type SpateClientSpawner struct {
	server_address string
	packet_size    int
}

// e.g. NewSpateClientSpawner("16-ffaa:0:1001,[172.31.0.23]:1337")
func NewSpateClientSpawner(server_address string) SpateClientSpawner {
	return SpateClientSpawner{
		server_address: server_address,
		packet_size:    1208,
	}
}

func (s SpateClientSpawner) ServerAddress(server_address string) SpateClientSpawner {
	s.server_address = server_address
	return s
}

func (s SpateClientSpawner) PacketSize(packet_size int) SpateClientSpawner {
	s.packet_size = packet_size
	return s
}

func (s SpateClientSpawner) Spawn() error {
	Info("Resolving address %s...", s.server_address)
	serverAddr, err := appnet.ResolveUDPAddr(s.server_address)
	if err != nil {
		Error("Resolution of UDP address (%s) failed: %v", s.server_address, err)
		return err
	}

	//TODO: Select fitting metric
	// Defined in appnet
	// const (
	//         PathAlgoDefault = iota // default algorithm
	//         MTU                    // metric for path with biggest MTU
	//         Shortest               // metric for shortest path
	// )
	var metric = 0 // Default

	Info("Selecting paths to remote...")
	path, err := appnet.ChoosePathByMetric(metric, serverAddr.IA)
	if err != nil {
		Warn("Could not choose path with metric: %v", err)
	}
	if path != nil {
		// Set selected singular path
		Info("Choosing the following path: %v", path)
		appnet.SetPath(serverAddr, path)
	}

	Info("Establishing connection with server...")
	DCConn, err := appnet.DialAddr(serverAddr)
	rand := NewFastRand(uint64(s.packet_size))
	start := time.Now()
	Info("Starting sending data for measurements")
	for {
		_, err = DCConn.Write(*rand.Get())
		if err != nil {
			Info("Sending data failed, assuming server finished measurements: %v", err)
			break
		}
	}
	elapsed := time.Since(start)
	Info("Finished sending packets, operation took %s", elapsed)

	return nil
}
