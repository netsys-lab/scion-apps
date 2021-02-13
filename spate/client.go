package main

import (
	"os"
	"os/signal"
	"time"

	"github.com/fatih/color"
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
		// Use first available path
		Info("Using single path")
		paths = paths[:1]
	}
	Info("Choosing the following paths: %v", paths)
	Info("Establishing connections with server...")

	bytes_sent := 0
	packets_sent := 0
	complete := make(chan struct{})
	var conns []*snet.Conn
	for _, path := range paths {
		// Set selected singular path
		Info("Creating new connection on path %v...", path)
		appnet.SetPath(serverAddr, path)
		conn, err := appnet.DialAddr(serverAddr)
		// Checking on err != nil will not work here as non-critical errors are returned
		if conn != nil {
			go awaitCompletion(conn, complete)
			conns = append(conns, conn)
		} else {
			Warn("Connection on path %v failed: %v", path, err)
		}
	}

	counter := make(chan int)
	cancel := make(chan os.Signal, 1)
	signal.Notify(cancel, os.Interrupt)

	Info("Starting sending data for measurements...")
	start := time.Now()
	for _, conn := range conns {
		// Spawn new thread
		go workerThread(conn, counter, s)
	}

runner:
	for {
		select {
		case bytes := <-counter:
			bytes_sent += bytes
			packets_sent += 1
		case <-cancel:
			Info("Received interrupt signal, stopping flooding of available paths...")
			break runner
		case <-complete:
			Info("Measurements finished on server!")
			break runner
		}
	}

	elapsed := time.Since(start)

	heading := color.New(color.Bold, color.Underline).Sprint("Summary")
	deco := color.New(color.Bold).Sprint("=====")
	Info("      %s %s %s", deco, heading, deco)
	Info("     Sent data: %v KiB", bytes_sent/1024.0)
	Info("  Sent packets: %v packets", packets_sent)
	Info("   Packet size: %v B", s.packet_size)
	Info("      Duration: %s", elapsed)

	return nil
}

func workerThread(conn *snet.Conn, counter chan int, spawner SpateClientSpawner) {
	rand := NewFastRand(uint64(spawner.packet_size))
	// TODO: add optional bandwidth control via command line option
	for {
		sent_bytes, err := conn.Write(*rand.Get())
		if err != nil {
			Error("Sending data failed: %v", err)
			break
		}
		counter <- sent_bytes
	}
}

func awaitCompletion(conn *snet.Conn, complete chan struct{}) {
	buf := make([]byte, 4)
	for {
		_, err := conn.Read(buf)
		if err != nil {
			Error("Waiting for completion of measurement failed: %v", err)
			close(complete)
			break
		}
		if string(buf) == "stop" {
			close(complete)
			break
		}
	}
}
