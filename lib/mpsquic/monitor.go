package mpsquic

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strings"
	"time"

	"github.com/lucas-clemente/quic-go"
	"github.com/lucas-clemente/quic-go/qerr"

	"github.com/scionproto/scion/go/lib/common"
	"github.com/scionproto/scion/go/lib/ctrl/path_mgmt"
	"github.com/scionproto/scion/go/lib/hpkt"
	"github.com/scionproto/scion/go/lib/pathmgr"
	"github.com/scionproto/scion/go/lib/pathpol"
	"github.com/scionproto/scion/go/lib/scmp"
	"github.com/scionproto/scion/go/lib/spath"
	"github.com/scionproto/scion/go/lib/spath/spathmeta"
	"github.com/scionproto/scion/go/lib/spkt"
	"github.com/scionproto/scion/go/tools/scmp/cmn"
)

var _ quic.Stream = (*monitoredStream)(nil)

type monitoredStream struct {
	quic.Stream
	underlayConn *MPQuic
}

func (ms monitoredStream) Write(p []byte) (n int, err error) {
	//streamID := ms.Stream.StreamID()
	activeAtWriteStart := ms.underlayConn.active
	start := time.Now()
	n, err = ms.Stream.Write(p)
	if err != nil {
		if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
			fmt.Println("Stream timeout", "err", err)
			return
		}
		if qErr, ok := err.(*qerr.QuicError); ok {
			if qErr.ErrorCode == qerr.NetworkIdleTimeout || qErr.ErrorCode == qerr.PeerGoingAway {
				// Remote went away
				fmt.Println("Stream error", "err", err)
				return 0, qErr
			}
		}
		fmt.Println("monitoredStream error", err)
	}
	elapsed := time.Now().Sub(start)
	bandwidth := len(p) * 8 * 1e9 / int(elapsed)
	// Check if the path remained the same
	if ms.underlayConn.active == activeAtWriteStart {
		activeAtWriteStart.bw = bandwidth
	}
	return
}

func (ms monitoredStream) Read(p []byte) (n int, err error) {
	n, err = ms.Stream.Read(p)
	if err != nil {
		if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
			fmt.Println("Stream timeout", "err", err)
			return
		}
		if qErr, ok := err.(*qerr.QuicError); ok {
			if qErr.ErrorCode == qerr.NetworkIdleTimeout || qErr.ErrorCode == qerr.PeerGoingAway {
				// Remote went away
				fmt.Println("Stream error", "err", err)
				return 0, qErr
			}
		}
		fmt.Println("monitoredStream error", err)
	}
	return
}

// monitor monitors the paths of mpq by sending SCMP messages at regular intervals and recording the replies in separate goroutines.
// It manages path expiration and path change decisions.
func (mpq *MPQuic) monitor() {
	cmn.Remote = *mpq.scionFlexConnection.raddr
	cmn.Local = *mpq.scionFlexConnection.laddr
	if cmn.Stats == nil {
		cmn.Stats = &cmn.ScmpStats{}
	}

	go mpq.sendSCMP()
	go mpq.rcvSCMP()

	go mpq.managePaths()
}

// sendSCMP sends SCMP messages on all paths in mpq.
func (mpq *MPQuic) sendSCMP() {
	var seq uint16
	for {
		if mpq.dispConn == nil {
			break
		}

		for i := range mpq.paths {
			cmn.Remote = *mpq.paths[i].raddr
			id := uint64(i + 1)
			info := &scmp.InfoEcho{Id: id, Seq: seq}
			pkt := cmn.NewSCMPPkt(scmp.T_G_EchoRequest, info, nil)
			b := make(common.RawBytes, mpq.paths[i].path.Entry.Path.Mtu)
			nhAddr := cmn.NextHopAddr()

			nextPktTS := time.Now()
			cmn.UpdatePktTS(pkt, nextPktTS)
			// Serialize packet to internal buffer
			pktLen, err := hpkt.WriteScnPkt(pkt, b)
			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "ERROR: Unable to serialize SCION packet. err=%v\n", err)
				break
			}
			written, err := mpq.dispConn.WriteTo(b[:pktLen], nhAddr)
			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "ERROR: Unable to write. err=%v\n", err)
				break
			} else if written != pktLen {
				_, _ = fmt.Fprintf(os.Stderr, "ERROR: Wrote incomplete message. written=%d, expected=%d\n",
					len(b), written)
				break
			}
			cmn.Stats.Sent += 1

			payload := pkt.Pld.(common.RawBytes)
			_, _ = info.Write(payload[scmp.MetaLen:])
			seq += 1
			//fmt.Println("Sent SCMP packet, len:", pktLen, "payload", payload, "ID", info.Id)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// rcvSCMP receives SCMP messages and records the RTT for each path in mpq.
func (mpq *MPQuic) rcvSCMP() {
	for {
		if mpq.dispConn == nil {
			break
		}

		pkt := &spkt.ScnPkt{}
		b := make(common.RawBytes, 1500)

		pktLen, err := mpq.dispConn.Read(b)
		if err != nil {
			if common.IsTimeoutErr(err) {
				continue
			} else {
				if strings.Contains(err.Error(), "use of closed network connection") {
					_, _ = fmt.Fprintf(os.Stderr, "INFO: Unable to read SCMP reply. Network down.\n")
					break
				}
				_, _ = fmt.Fprintf(os.Stderr, "ERROR: Unable to read SCMP reply. err=%v\n", err)
				break
			}
		}
		now := time.Now()
		err = hpkt.ParseScnPkt(pkt, b[:pktLen])
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "ERROR: SCION packet parse. err=%v\n", err)
			continue
		}
		// Validate scmp packet
		scmpHdr, ok := pkt.L4.(*scmp.Hdr)
		if !ok {
			_, _ = fmt.Fprintf(os.Stderr, "ERROR: Not an SCMP header. type=%v\n", common.TypeOf(pkt.L4))
			continue
		}
		scmpPld, ok := pkt.Pld.(*scmp.Payload)
		if !ok {
			_, _ = fmt.Fprintf(os.Stderr, "ERROR: Not an SCMP payload. type=%v\n", common.TypeOf(pkt.Pld))
			continue
		}

		switch scmpPld.Info.(type) {
		case *scmp.InfoRevocation:
			pathKey, err := getReversePathKey(*pkt.Path)
			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "ERROR: Unable to map revocation to path key. err=%v\n", err)
				continue
			}
			mpq.handleSCMPRevocation(scmpPld.Info.(*scmp.InfoRevocation), pathKey)
		case *scmp.InfoEcho:
			cmn.Stats.Recv += 1
			// Calculate RTT
			rtt := now.Sub(scmpHdr.Time()).Round(time.Microsecond)
			scmpId := scmpPld.Info.(*scmp.InfoEcho).Id
			if scmpId-1 < uint64(len(mpq.paths)) {
				//fmt.Println("Received SCMP packet, len:", pktLen, "ID", info.Id)
				mpq.paths[scmpId-1].rtt = rtt
			} else {
				_, _ = fmt.Fprintf(os.Stderr, "ERROR: Wrong InfoEcho Id. id=%v\n", scmpId)
			}
		default:
			_, _ = fmt.Fprintf(os.Stderr, "ERROR: Not an Info Echo. type=%v\n", common.TypeOf(scmpPld.Info))
		}
	}
}

// getReversePathKey gets an AppPath key for path after reversing it.
func getReversePathKey(path spath.Path) (pk *spathmeta.PathKey, err error) {
	err = path.Reverse()
	if err != nil {
		return nil, err
	}
	appPath, err := mockAppPath(&path, nil)
	if err != nil {
		return nil, err
	}
	appPathKey := appPath.Key()
	return &appPathKey, nil
}

// handleSCMPRevocation revocation handles explicit revocation notification of a link on a path being probed
// The active path is switched if the revocation expiration is in the future and was issued for an interface on the active path.
// If the revocation expiration is in the future, but for a backup path, the only the expiration time of the path is set to the current time.
func (mpq *MPQuic) handleSCMPRevocation(revocation *scmp.InfoRevocation, pk *spathmeta.PathKey) {
	signedRevInfo, err := path_mgmt.NewSignedRevInfoFromRaw(revocation.RawSRev)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "ERROR: Unable to decode SignedRevInfo from SCMP InfoRevocation payload. err=%v\n", err)
	}
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "ERROR: Failed to decode SCMP signed revocation Info. err=%v\n", err)
	}
	ri, err := signedRevInfo.RevInfo()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "ERROR: Failed to decode SCMP revocation Info. err=%v\n", err)
	}
	if ri.Expiration().After(time.Now()) {
		for i, pathInfo := range mpq.paths {
			if pathInfo.path.Key() == *pk {
				mpq.paths[i].expiration = time.Now()
			}
		}
	} else {
		// Ignore expired revocations
		fmt.Println("Processing revocation", "Ignoring expired revocation.")
	}
	if *pk == mpq.active.path.Key() {
		err := mpq.switchMPConn(true, false)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "ERROR: Failed to switch path after path revocation. err=%v\n", err)
		}
	}
}

// refreshPaths requests sciond for updated paths
func (mpq *MPQuic) refreshPaths(resolver pathmgr.Resolver) {
	var filter *pathpol.Policy = nil
	sciondTimeout := 3 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), sciondTimeout)
	defer cancel()
	syncPathMonitor, err := resolver.WatchFilter(ctx, mpq.network.IA(), mpq.paths[0].raddr.IA, filter)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "ERROR: Failed to monitor paths. src=%v, dst=%v, filter=%v\n",
			mpq.network.IA(), mpq.paths[0].raddr.IA, filter)
		syncPathMonitor = nil
	}

	if syncPathMonitor == nil {
		return
	}

	syncPathsData := syncPathMonitor.Load()
	for pathIndex, expiringPathInfo := range mpq.paths {
		selectionKey := expiringPathInfo.path.Key()
		appPath := syncPathsData.APS.GetAppPath(selectionKey)
		if appPath.Key() != selectionKey {
			_, _ = fmt.Fprintf(os.Stderr, "DEBUG: Failed to refresh path, key does not match. Retrying later.\n")
			//_, _ = fmt.Fprintf(os.Stderr, "INFO: src=%v, dst=%v, key=%v, path=%v, filter=%v\n",
			//	mpq.network.IA(), mpq.paths[0].raddr.IA, selectionKey, expiringPathInfo.path.Entry.Path.Interfaces, filter)
		} else {
			freshExpTime := appPath.Entry.Path.Expiry()
			if freshExpTime.After(mpq.paths[pathIndex].expiration) {
				mpq.paths[pathIndex].path = *appPath

				// Update the path on the remote address
				newPath := spath.New(appPath.Entry.Path.FwdPath)
				_ = newPath.InitOffsets()
				tmpRaddr := mpq.paths[pathIndex].raddr.Copy()
				tmpRaddr.Path = newPath
				tmpRaddr.NextHop, _ = appPath.Entry.HostInfo.Overlay()
				mpq.paths[pathIndex].raddr = tmpRaddr
				mpq.paths[pathIndex].path = spathmeta.AppPath{appPath.Entry}
				mpq.paths[pathIndex].expiration = mpq.paths[pathIndex].path.Entry.Path.Expiry()
			} else {
				_, _ = fmt.Fprintf(os.Stderr, "DEBUG: Refreshed path does not have later expiry. Retrying later.\n")
				//_, _ = fmt.Fprintf(os.Stderr, "INFO: src=%v, dst=%v, key=%v, path=%v, filter=%v, currExp=%v, freshExp=%v\n",
				//	mpq.network.IA(), mpq.paths[0].raddr.IA, selectionKey, expiringPathInfo.path.Entry.Path.Interfaces,
				//	filter, mpq.paths[pathIndex].expiration, freshExpTime)
			}
		}
	}
}

// earliestPathExpiry computes the earliest expiration time of any path registered in mpq.
func (mpq *MPQuic) earliestPathExpiry() (ret time.Time) {
	ret = time.Now().Add(maxDuration)
	for _, pathInfo := range mpq.paths {
		if pathInfo.expiration.Before(ret) {
			ret = pathInfo.expiration
		}
	}
	return
}

// managePaths evaluates every 5 seconds if a path is about to expire and if there is a better path to switch to.
func (mpq *MPQuic) managePaths() {
	lastUpdate := time.Now()
	pr := mpq.network.PathResolver()
	// Get initial expiration time of all paths
	for i, pathInfo := range mpq.paths {
		mpq.paths[i].expiration = pathInfo.path.Entry.Path.Expiry()
	}

	// Busy wait until we have at least measurements on two paths
	for {
		var measuredPaths int
		for _, pathInfo := range mpq.paths {
			if pathInfo.rtt != maxDuration {
				measuredPaths += 1
			}
		}
		if measuredPaths > 1 {
			break
		}
	}

	// Make a (voluntary) path change decision to increase performance at most once per 5 seconds
	// Use the time in between to refresh the path information if required
	var maxFlap time.Duration = 5 * time.Second
	for {
		if mpq.dispConn == nil {
			break
		}

		earliesExp := mpq.earliestPathExpiry()
		// Refresh the paths if one of them expires in less than 10 minutes
		if earliesExp.Before(time.Now().Add(10*time.Minute - time.Duration(rand.Intn(10))*time.Second)) {
			mpq.refreshPaths(pr)
		}

		sinceLastUpdate := time.Now().Sub(lastUpdate)
		time.Sleep(maxFlap - sinceLastUpdate) // Failing paths are handled separately / faster
		err := mpq.switchMPConn(false, true)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "ERROR: Failed to switch path. err=%v\n", err)
		}
		lastUpdate = time.Now()
	}
}
