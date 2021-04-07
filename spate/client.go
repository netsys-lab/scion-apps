package main

import (
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/netsec-ethz/scion-apps/pkg/appnet"
	"github.com/scionproto/scion/go/lib/snet"
)

type SpateClientSpawner struct {
	server_address string
	packet_size    int
	single_path    bool
	bandwidth      int64
}

// e.g. NewSpateClientSpawner("16-ffaa:0:1001,[172.31.0.23]:1337")
func NewSpateClientSpawner(server_address string) SpateClientSpawner {
	return SpateClientSpawner{
		server_address: server_address,
		packet_size:    1208,
		single_path:    false,
		bandwidth:      0,
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

func (s SpateClientSpawner) Bandwidth(bandwidth int64) SpateClientSpawner {
	s.bandwidth = bandwidth
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
		Warn("Detected test on direct connection. Multipath via SCION is not available...")
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
	complete := make(chan struct{}, len(paths))
	var conns []*snet.Conn
	for _, path := range paths {
		// Set selected singular path
		Info("Creating new connection on path %v...", path)
		appnet.SetPath(serverAddr, path)
		for i :=0; i < 8; i++ {
			conn, err := appnet.DialAddrUDP(serverAddr)
			// Checking on err != nil will not work here as non-critical errors are returned
			if conn != nil {
				go awaitCompletion(conn, complete)
				conns = append(conns, conn)
			} else {
				Warn("Connection on path %v failed: %v", path, err)
			}
		}
	}

	counter := make(chan int, 1024)
	stop := make(chan struct{}, 1)
	cancel := make(chan os.Signal, 1)
	signal.Notify(cancel, os.Interrupt)

	Info("Starting sending data for measurements...")
	start := time.Now()
	var wg sync.WaitGroup
	for _, conn := range conns {
		// Spawn new thread
		wg.Add(1)
		Info("Spawn Connection!")
		go workerThread(conn, counter, stop, &wg, s)
	}

	closed_conn := 0
	total_conn := len(conns)
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
			closed_conn += 1
			if closed_conn >= total_conn {
				Info("Measurements finished on server!")
				break runner
			}
		}
	}

	elapsed := time.Since(start)
	actual_bandwidth := float64(bytes_sent) / elapsed.Seconds() * 8.0 / 1024.0 / 1024.0

	stop <- struct{}{}
	//wg.Wait()

	heading := color.New(color.Bold, color.Underline).Sprint("Summary")
	deco := color.New(color.Bold).Sprint("=====")
	lower := color.New(color.Bold).Sprint("===================")
	Info("         %s %s %s", deco, heading, deco)
	Info("         Sent data: %v KiB", bytes_sent/1024.0)
	Info("      Sent packets: %v packets", packets_sent)
	Info("       Packet size: %v B", s.packet_size)
	Info("          Duration: %s", elapsed)
	Info("  Target bandwidth: %v Mib/s", float64(s.bandwidth)/1024.0/1024.0)
	Info("  Actual bandwidth: %v Mib/s", actual_bandwidth)
	Info("         %s", lower)
	Info(">>> Please check the server measurements for the throughput achieved through")
	Info(">>> the network!")

	return nil
}

func workerThread(conn *snet.Conn, counter chan int, stop chan struct{}, finalize *sync.WaitGroup, spawner SpateClientSpawner) {
	var sleep_duration int64
	defer finalize.Done()

	rand := NewFastRand(uint64(spawner.packet_size))
	control_points := make(chan BandwidthControlPoint, 1024)
	//go SimpleBandwidthControl(control_points, &sleep_duration, finalize, spawner)
	go PidBandwidthControl(control_points, &sleep_duration, finalize, spawner)

worker:
	for {
		select {
		case <-stop:
			break worker
		default:
			sent_bytes, err := conn.Write(*rand.Get())
			if err != nil {
				Error("Sending data failed: %v", err)
				break
			}

			control_points <- BandwidthControlPoint{sent_bytes: sent_bytes, timestamp: time.Now()}
			counter <- sent_bytes

			if sleep_duration > 0 {
				time.Sleep(time.Duration(sleep_duration))
			}
		}
	}
	//close(control_points)
}

func awaitCompletion(conn *snet.Conn, complete chan struct{}) {
	buf := make([]byte, 4)
	for {
		_, err := conn.Read(buf)
		if err != nil {
			Error("Waiting for completion of measurement failed: %v", err)
			complete <- struct{}{}
			break
		}
		if string(buf) == "stop" {
			complete <- struct{}{}
			break
		}
	}
}
