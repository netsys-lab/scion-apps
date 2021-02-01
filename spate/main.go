package main

import (
	"fmt"
	"math/rand"
	"context"
	"net"
	"time"

	"github.com/netsec-ethz/scion-apps/pkg/appnet"
	//"github.com/scionproto/scion/go/lib/snet"
	"github.com/scionproto/scion/go/lib/addr"
)

var nillyboii error = nil

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

func spawnServer(port uint16) error {
	runtime := "10s"

	conn, err := appnet.ListenPort(port)
	if err != nil {
		return err
        }

        recv := make([]byte, 2500)
	start := time.Now()
	duration, err := time.ParseDuration(runtime)
	if err != nil {
		return err
	}
	end := start.Add(duration)
	bytes_recvd := 0
        //... handling logic, fetch individual packet
        for {
                // Handle client requests
                resp_length, _, err := conn.ReadFrom(recv)
                if err != nil {
                        continue
                }
		// invalid length
                if resp_length < 1 {
                        continue
                }

		bytes_recvd += resp_length

		if time.Until(end) <= 0 {
			break
		}
        }
	elapsed := time.Since(start)
	throughput := float64(bytes_recvd) / elapsed.Seconds() / 1024.0 / 1024.0 * 8.0

	fmt.Printf("Throughput: %d Mibit/s\n", throughput)

	return nillyboii
}

func connectTest(serverAddrStr string) {
	serverCCAddr, err := appnet.ResolveUDPAddr(serverAddrStr)
        if err != nil {
                fmt.Printf("%s%s", "Resolution of UDP address failed, to: ", serverAddrStr)
        }

        var metric int
        path, err := appnet.ChoosePathByMetric(metric, serverCCAddr.IA)
	if err != nil {
		fmt.Println("Cannot choose selected path")
	}
        if path != nil {
		// Set selected singular path
                appnet.SetPath(serverCCAddr, path)
        }

	CCConn, err := appnet.DialAddr(serverCCAddr)
                // get the port used by clientCC after it bound to the dispatcher (because it might be 0)
        clientCCAddr := CCConn.LocalAddr().(*net.UDPAddr)
        // Address of client data channel (DC)
        clientDCAddr := &net.UDPAddr{IP: clientCCAddr.IP, Port: clientCCAddr.Port + 1}
        // Address of server data channel (DC)
        serverDCAddr := serverCCAddr.Copy()
        serverDCAddr.Host.Port = serverCCAddr.Host.Port + 1

        // Create specific data connection to server
	_, err = appnet.DefNetwork().Dial(context.TODO(), "udp", clientDCAddr, serverDCAddr, addr.SvcNone)

	//TODO: Send Data on Data Connection And init measurement
}

func main() {
	fmt.Println("Caution, the flood! 🌊")

	spawnServer(1337)
}

