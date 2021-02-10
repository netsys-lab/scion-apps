package main

import (
	"time"
	"os"
	"os/signal"

	"github.com/netsec-ethz/scion-apps/pkg/appnet"
	"github.com/scionproto/scion/go/lib/snet"
)

type SpateClientSpawner struct {
	server_address string
	packet_size    int
	single_path    bool
}

// e.g. NewSpateClientSpawner("16-ffaa:0:1001,[172.31.0.23]:1337")
func NewSpateClientSpawner(server_address string) SpateClientSpawner {
	return SpateClientSpawner{
		server_address: server_address,
		packet_size:    1208,
		single_path:    false,
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

func (s SpateClientSpawner) SinglePath(single_path bool) SpateClientSpawner {
	s.single_path = single_path
	return s
}

func (s SpateClientSpawner) Spawn() error {
	Info("Resolving address %s...", s.server_address)
	serverAddr, err := appnet.ResolveUDPAddr(s.server_address)
	if err != nil {
		Error("Resolution of UDP address (%s) failed: %v", s.server_address, err)
		return err
	}

	Info("Searching paths to remote...")
	paths, err := appnet.QueryPaths(serverAddr.IA)
	if err != nil {
		Warn("Could not query for available paths: %v", err)
		Error("Could not find valid paths!")
		os.Exit(1)
	}
	if paths == nil {
		Warn("Detected test on localhost. Multipath is not available...")
		paths = []snet.Path{nil}
	}
	if s.single_path {
		// Use default path, if not defined further scion chooses the first available path
		Info("Using single path")
		paths = []snet.Path{nil}
	}
	Info("Choosing the following paths: %v", paths)
	Info("Establishing connections with server...")
	for _, path := range paths {
		// Set selected singular path
		Info("Creating new connection thread for available path")
		appnet.SetPath(serverAddr, path)
		conn, _ := appnet.DialAddr(serverAddr)
		// Checking on err != nil will not work here as non-critical errors are returned
		if conn != nil {
			// Dial connection and spawn new thread
			go workerThread(conn, s.packet_size)
		}
	}
	Info("Starting sending data for measurements")

	start := time.Now()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	_ = <-c
	Info("Received interrupt signal, stopping flooding of available paths...")

	elapsed := time.Since(start)
	Info("Finished sending packets, operation took %s", elapsed)
	os.Exit(0)

	return nil
}

func workerThread(conn *snet.Conn, pkt int) {
	rand := NewFastRand(uint64(pkt))
	// TODO: add optional bandwidth control via command line option
	for {
		_, err := conn.Write(*rand.Get())
		if err != nil {
			Info("Sending data failed, assuming server finished measurements: %v", err)
			break
		}
	}
}
