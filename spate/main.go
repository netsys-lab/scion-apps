package main

import (
	"fmt"
	"math/rand"
	"context"
	"net"

	"github.com/netsec-ethz/scion-apps/pkg/appnet"
	"github.com/scionproto/scion/go/lib/snet"
	"github.com/scionproto/scion/go/lib/addr"
)

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
	conn, err := appnet.ListenPort(port)
	if err != nil {
		return err
        }

        recv := make([]byte, 2500)

        //... handling logic, fetch individual packet
        for {
                // Handle client requests
                resp_length, fromAddr, err := conn.ReadFrom(recv)
                // Save for final results
                clientCCAddr := fromAddr.(*snet.UDPAddr)
                if err != nil {
                        continue
                }
		// invalid length
                if resp_length < 1 {
                        continue
                }
		// TODO: Handle receiving of data
        }
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
	DCConn, err := appnet.DefNetwork().Dial(context.TODO(), "udp", clientDCAddr, serverDCAddr, addr.SvcNone)

	//TODO: Send Data on Data Connection And init measurement
}

func main() {
	fmt.Println("Caution, the flood! 🌊")

	var frand = NewFastRand()
	num := frand.Get()
	fmt.Printf("%x\n", num)
}

