package main

import (
	"net"
	"os"
	"fmt"
	"os/signal"
	"time"

	"github.com/fatih/color"
	"github.com/netsec-ethz/scion-apps/pkg/appnet"
	"github.com/scionproto/scion/go/lib/snet"
)

type AsyncReadResult struct {
	resp_length int
	err         error
}

type IntervalElement struct {
	bytes	int
	time    time.Time
}

func asyncConnRead(conn *snet.Conn, recvbuf []byte) chan AsyncReadResult {
	recv := make(chan AsyncReadResult, 1)
	go func() {
		resp_length, err := conn.Read(recvbuf)
		recv <- AsyncReadResult{resp_length: resp_length, err: err}
	}()
	return recv
}

type AsyncReadFromResult struct {
	resp_length int
	addr        net.Addr
	err         error
}

func asyncConnReadFrom(conn *snet.Conn, recvbuf []byte) chan AsyncReadFromResult {
	recv := make(chan AsyncReadFromResult, 1)
	go func() {
		resp_length, addr, err := conn.ReadFrom(recvbuf)
		recv <- AsyncReadFromResult{resp_length: resp_length, addr: addr, err: err}
	}()
	return recv
}

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

func tock(recv_bytes chan int, intervals chan IntervalElement) {
	bytes := 0
	elements_at_start := len(recv_bytes)
    start_time := time.Now()
	for idx := 0; idx < elements_at_start; idx++ {
		bytes += <- recv_bytes;
	}
	intervals <- IntervalElement{bytes, start_time}
}

func clock(recv_bytes chan int, intervals chan IntervalElement, stop chan struct{}) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

loop:
	for {
		select {
		case _ = <- ticker.C:
			tock(recv_bytes, intervals)
		case _ = <- stop:
			// Clear channel our for last chunk
			tock(recv_bytes, intervals)
			close(intervals)
			break loop
		}
	}
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


	recv_bytes_channel := make(chan int, 30720)
	intervals := make(chan IntervalElement, s.runtime_duration.Milliseconds() / 100)
	stop := make(chan struct{}, 1)
	go clock(recv_bytes_channel, intervals, stop)

	start := time.Now()
	end := time.After(s.runtime_duration)
	//... handling logic, fetch individual packet
runner:
	for {
		select {
		// Handle client requests
		case read_result := <-asyncConnRead(conn, recvbuf):
			if read_result.err != nil {
				Warn("An error occurred while receiving remote packets: %s", err)
				continue
			}

			// invalid length
			if read_result.resp_length != s.packet_size {
				Warn("Received packet was of wrong size: got %d but expected %d", read_result.resp_length, s.packet_size)
				continue
			}

			bytes_received += read_result.resp_length
			recv_bytes_channel <- read_result.resp_length
			packets_received += 1
		case <-end:
			Info("End!")
			break runner
		case <-cancel:
			Info("Received interrupt signal, canceling measurements...")
			break runner
		}
	}

	Info("Measurements finished!")
	elapsed := time.Since(start)
	stop <- struct{}{}
	throughput := float64(bytes_received) / elapsed.Seconds() * 8.0 / 1024.0 / 1024.0

	Info("Notifying clients to stop sending...")
	remote_addrs := make(map[net.Addr]bool)
	timeout := time.After(100 * time.Millisecond)
	// This loop will receive packets for an additional 100ms to gather all
	// the clients which must be notified of the finished measurements.
	// This is not done in the above runner as the map operations below cost
	// ~40Mibit/s.
notifier:
	for {
		select {
		case read_from_result := <-asyncConnReadFrom(conn, recvbuf):
			remote_addrs[read_from_result.addr] = true
		case <-timeout:
			break notifier
		}
	}
	for addr := range remote_addrs {
		conn.WriteTo([]byte("stop"), addr)
	}

	file, err := os.Create("measure.csv")
	if err != nil {
		Error("Could not create file for measurements")
	}
	file.Write([]byte("time,bytes\n"))
	for itv := range intervals {
		elapsed := itv.time.Sub(start)
		file.Write([]byte(fmt.Sprintf("%d,%d\n", elapsed.Milliseconds(), itv.bytes)))
	}


	heading := color.New(color.Bold, color.Underline).Sprint("Measurement Results")
	deco := color.New(color.Bold).Sprint("=====")
	lower := color.New(color.Bold).Sprint("===============================")
	Info("    %s %s %s", deco, heading, deco)
	Info("     Received data: %v KiB", bytes_received/1024.0)
	Info("  Received packets: %v packets", packets_received)
	Info("       Packet size: %v B", s.packet_size)
	Info("          Duration: %s", elapsed)
	Info("        Throughput: %v Mib/s", throughput)
	Info("    %s", lower)

	return nil
}
